package serve

import (
	"bufio"
	"cronrunner/pkg/conf"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
)

const NOT_STARTED = "NOT_STARTED"
const CREATED = "CREATED"
const RUNNING = "RUNNING"
const FAILED = "FAILED"
const SUCCESSFUL = "SUCCESSFUL"

type Job struct {
	Id                 string
	Command            string
	Spec               string
	LastRunStatus      string
	Args               []string
	LastRun            *JobRun
	SuccessfulRunCount int
	FailedRunCount     int
}

func NewJob(conf *conf.JobConfig) *Job {
	return &Job{
		Id:            conf.Id,
		Command:       conf.Command,
		Spec:          conf.Spec,
		Args:          conf.Args,
		LastRunStatus: NOT_STARTED,
	}
}

type JobRun struct {
	Command          string
	Args             []string
	Status           string
	Duration         int64
	StartTime        time.Time
	EndTime          time.Time
	Error            error `json:"Error,omitempty`
	StdOutFilePath   string
	StdErrorFilePath string
}

func NewJobRun(job *Job, stdoutFilePath string, stderrFilePath string) *JobRun {
	return &JobRun{
		Command:          job.Command,
		Args:             job.Args,
		Status:           NOT_STARTED,
		StdOutFilePath:   stdoutFilePath,
		StdErrorFilePath: stderrFilePath,
	}
}

func (jobRun *JobRun) Run() error {
	jobRun.StartTime = time.Now()
	jobRun.Status = RUNNING
	err := jobRun.Exec()
	if err != nil {
		jobRun.Status = FAILED
	} else {
		jobRun.Status = SUCCESSFUL
	}
	jobRun.EndTime = time.Now()
	jobRun.Duration = jobRun.EndTime.Sub(jobRun.StartTime).Milliseconds()
	return err
}

func (jobRun *JobRun) Exec() error {
	cmd := exec.Command(jobRun.Command, jobRun.Args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	stderrScanner := bufio.NewScanner(stderr)
	go func() {
		if err := ScanerToFile(stderrScanner, jobRun.StdErrorFilePath); err != nil {
			logError(err.Error())
		}
	}()

	stdoutScanner := bufio.NewScanner(stdout)
	go func() {
		if err := ScanerToFile(stdoutScanner, jobRun.StdOutFilePath); err != nil {
			logError(err.Error())
		}
	}()

	return cmd.Wait()
}

func ScanerToFile(scanner *bufio.Scanner, filePath string) error {
	var stdoutFile *os.File
	var err error

	for scanner.Scan() {
		if stdoutFile == nil {
			stdoutFile, err = os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			defer stdoutFile.Close()
		}
		if err := stdoutFile.Sync(); err != nil {
			return err
		}

		if _, err := stdoutFile.Write([]byte(scanner.Text())); err != nil {
			return err
		}
	}
	return nil
}

func logError(format string, a ...any) {
	_, err := fmt.Fprintf(os.Stderr, format, a...)
	if err != nil {
		panic(err)
	}
}

func Invoke(configPath string, bindAddress string) error {
	conf, err := conf.LoadFromYAML[conf.Configuration](configPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(conf.LogDir, os.ModePerm); err != nil {
		return err
	}

	logBaseDir := conf.LogDir

	logPath := filepath.Join(logBaseDir, "log.text")
	appLogFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer appLogFile.Close()

	var appLogFunc = func(format string, a ...any) error {
		_, err := appLogFile.Write([]byte(
			fmt.Sprintf(format, a...),
		))
		return err
	}

	c := cron.New()

	var jobs []*Job

	for idx := range conf.Jobs {
		job := NewJob(&conf.Jobs[idx])
		jobs = append(jobs, job)

		if _, err := c.AddFunc(job.Spec, func() {
			if err := appLogFunc("%s\t%s\tSTART\n", time.Now(), job.Id); err != nil {
				logError("%s\t%s\t%s", time.Now(), job.Id, err.Error())
				return
			}
			if err := appLogFile.Sync(); err != nil {
				logError("%s\t%s\tError=%s", time.Now(), job.Id, err.Error())
				return
			}

			jobLogDir := filepath.Join(logBaseDir, job.Id)

			if err := os.MkdirAll(jobLogDir, os.ModePerm); err != nil {
				logError("%s\t%s\tError=%s", time.Now(), job.Id, err.Error())
				return
			}

			fileName := time.Now().Format(time.RFC3339)
			stdoutFilePath := filepath.Join(jobLogDir, fmt.Sprintf("%s-%s.stdout.txt", job.Id, fileName))
			stderrFilePath := filepath.Join(jobLogDir, fmt.Sprintf("%s-%s.stderr.txt", job.Id, fileName))

			jobRun := NewJobRun(job, stdoutFilePath, stderrFilePath)
			job.LastRun = jobRun
			job.LastRunStatus = RUNNING
			if err := jobRun.Run(); err != nil {
				logError("%s\t%s\tError=%s", time.Now(), job.Id, err.Error())
				job.LastRunStatus = FAILED
				job.FailedRunCount += 1
			} else {
				job.LastRunStatus = SUCCESSFUL
				job.SuccessfulRunCount += 1
			}

			if err := appLogFunc("%s\t%s\tLastRunStatus=%s\n", time.Now(), job.Id, job.LastRunStatus); err != nil {
				logError("%s\t%s\tError=%s", time.Now(), job.Id, err.Error())
				return
			}

			if err := appLogFile.Sync(); err != nil {
				logError("%s\t%s\tError=%s", time.Now(), job.Id, err.Error())
				return
			}
		}); err != nil {
			return err
		}
	}

	go func() {
		r := gin.Default()
		r.GET("/ping", func(ctx *gin.Context) {
			ctx.JSON(http.StatusOK, gin.H{
				"message": "pong",
			})
		})
		r.GET("/jobs", func(ctx *gin.Context) {
			ctx.JSON(200, jobs)
		})

		if err := r.Run(bindAddress); err != nil {
			panic(err)
		}
	}()

	c.Run()
	return nil
}
