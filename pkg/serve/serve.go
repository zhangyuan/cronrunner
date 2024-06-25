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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"
	"github.com/ztrue/tracerr"
)

const NOT_STARTED = "NOT_STARTED"
const CREATED = "CREATED"
const RUNNING = "RUNNING"
const FAILED = "FAILED"
const SUCCEEDED = "SUCCESSFUL"

type Job struct {
	LastRun              *JobRun
	Id                   string
	Command              string
	WorkingDir           string
	Shell                string
	Spec                 string
	Env                  []string
	JobRunCountSucceeded int
	JobRunCountFailed    int
}

func NewJob(conf *conf.JobConfig) *Job {
	return &Job{
		Id:         conf.Id,
		Command:    conf.Command,
		WorkingDir: conf.WorkingDir,
		Spec:       conf.Spec,
		Env:        conf.Env,
	}
}

type JobRun struct {
	StartTime        time.Time
	EndTime          time.Time
	Command          string
	Status           string
	StdOutFilePath   string
	StdErrorFilePath string
	WorkingDir       string
	Shell            string
	Args             []string
	Env              []string
	Duration         int64
}

func NewJobRun(job *Job, stdoutFilePath string, stderrFilePath string) *JobRun {
	return &JobRun{
		Command:          job.Command,
		Shell:            job.Shell,
		WorkingDir:       job.WorkingDir,
		Status:           NOT_STARTED,
		StdOutFilePath:   stdoutFilePath,
		StdErrorFilePath: stderrFilePath,
		Env:              job.Env,
	}
}

func (jobRun *JobRun) Run() error {
	jobRun.StartTime = time.Now()
	jobRun.Status = RUNNING
	err := jobRun.Exec()
	if err != nil {
		jobRun.Status = FAILED
	} else {
		jobRun.Status = SUCCEEDED
	}
	jobRun.EndTime = time.Now()
	jobRun.Duration = jobRun.EndTime.Sub(jobRun.StartTime).Milliseconds()
	if err != nil {
		return tracerr.Wrap(err)
	}
	return nil
}

func NewLogFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
}

func (jobRun *JobRun) Exec() error {
	cmd := exec.Command(jobRun.Shell, "-c", jobRun.Command)
	cmd.Env = jobRun.Env
	cmd.Dir = jobRun.WorkingDir

	stderrFile, err := NewLogFile(jobRun.StdErrorFilePath)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer stderr.Close()

	if err != nil {
		return tracerr.Wrap(err)
	}
	defer stderrFile.Close()

	stderrScanner := bufio.NewScanner(stderr)

	stderrDone := make(chan string)

	go func() {
		if err := ScanerToFile(stderrScanner, stderrFile); err != nil {
			tracerr.PrintSourceColor(err)
		}
		close(stderrDone)
	}()

	stdoutFile, err := NewLogFile(jobRun.StdOutFilePath)
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer stdoutFile.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer stdout.Close()

	stdoutScanner := bufio.NewScanner(stdout)

	stdoutDone := make(chan string)
	go func() {
		if err := ScanerToFile(stdoutScanner, stdoutFile); err != nil {
			tracerr.PrintSourceColor(err)
		}
		close(stdoutDone)
	}()

	if err := cmd.Run(); err != nil {
		return tracerr.Wrap(err)
	}

	for {
		select {
		case _, ok := <-stderrDone:
			if !ok {
				stderrDone = nil
			}
		case _, ok := <-stdoutDone:
			if !ok {
				stdoutDone = nil
			}
		}

		if stderrDone == nil && stdoutDone == nil {
			break
		}
	}

	return nil
}

func ScanerToFile(scanner *bufio.Scanner, file *os.File) error {
	for scanner.Scan() {
		text := scanner.Text()
		logLine := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), text)

		if _, err := file.Write([]byte(logLine)); err != nil {
			return tracerr.Wrap(err)
		}

		if err := file.Sync(); err != nil {
			return tracerr.Wrap(err)
		}

	}

	return tracerr.Wrap(scanner.Err())
}

func Invoke(configPath string, bindAddress string) error {
	conf, err := conf.LoadFromYAML[conf.Configuration](configPath)
	if conf.Shell == "" {
		conf.Shell = "/bin/bash"
	}

	if err != nil {
		tracerr.Wrap(err)
	}

	if err := os.MkdirAll(conf.LogDir, os.ModePerm); err != nil {
		tracerr.Wrap(err)
	}

	logBaseDir := conf.LogDir

	logPath := filepath.Join(logBaseDir, "log.txt")
	appLogFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		tracerr.Wrap(err)
	}
	defer appLogFile.Close()

	var appLogFunc = func(format string, a ...any) error {
		_, err := appLogFile.Write([]byte(
			fmt.Sprintf(format, a...),
		))
		return tracerr.Wrap(err)
	}

	c := cron.New()

	var jobs []*Job

	for idx := range conf.Jobs {
		job := NewJob(&conf.Jobs[idx])
		job.Shell = conf.Shell
		jobs = append(jobs, job)

		measurementNamePrefix := "cronrunner"
		jobRunTotal := promauto.NewCounter(prometheus.CounterOpts{
			Name: fmt.Sprintf("%s_%s_job_run_total", measurementNamePrefix, job.Id),
		})

		jobRunSucceeded := promauto.NewCounter(prometheus.CounterOpts{
			Name: fmt.Sprintf("%s_%s_job_run_succeeded", measurementNamePrefix, job.Id),
		})

		jobRunFailed := promauto.NewCounter(prometheus.CounterOpts{
			Name: fmt.Sprintf("%s_%s_job_run_failed", measurementNamePrefix, job.Id),
		})

		cronFunc := func() {
			jobRunTotal.Inc()
			var run = func() error {
				if err := appLogFunc("%s\t%s\tSTART\n", time.Now(), job.Id); err != nil {
					return tracerr.Wrap(err)
				}
				if err := appLogFile.Sync(); err != nil {
					return tracerr.Wrap(err)
				}

				jobLogDir := filepath.Join(logBaseDir, job.Id)

				if err := os.MkdirAll(jobLogDir, os.ModePerm); err != nil {
					return tracerr.Wrap(err)
				}

				stdoutFilePath := filepath.Join(jobLogDir, "stdout.txt")
				stderrFilePath := filepath.Join(jobLogDir, "stderr.txt")

				jobRun := NewJobRun(job, stdoutFilePath, stderrFilePath)
				job.LastRun = jobRun
				if err := jobRun.Run(); err != nil {
					return tracerr.Wrap(err)
				}

				if err := appLogFunc("%s\t%s\tLastRunStatus=%s\n", time.Now(), job.Id, SUCCEEDED); err != nil {
					return tracerr.Wrap(err)
				}

				if err := appLogFile.Sync(); err != nil {
					return tracerr.Wrap(err)
				}
				return nil
			}
			if err := run(); err != nil {
				jobRunFailed.Inc()
				job.JobRunCountFailed += 1
				tracerr.PrintSourceColor(err)
			} else {
				jobRunSucceeded.Inc()
				job.JobRunCountSucceeded += 1
			}
		}
		if _, err := c.AddFunc(job.Spec, cronFunc); err != nil {
			return tracerr.Wrap(err)
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

		r.GET("/metrics", func(ctx *gin.Context) {
			promhttp.Handler().ServeHTTP(ctx.Writer, ctx.Request)
		})

		if err := r.Run(bindAddress); err != nil {
			panic(err)
		}
	}()

	c.Run()
	return nil
}
