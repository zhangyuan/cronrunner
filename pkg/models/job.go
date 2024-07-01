package models

import (
	"bufio"
	"cronrunner/pkg/conf"
	"fmt"
	"os"
	"os/exec"
	"time"

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
	Retry                int
}

func NewJob(conf *conf.JobConfig) *Job {
	return &Job{
		Id:         conf.Id,
		Command:    conf.Command,
		WorkingDir: conf.WorkingDir,
		Spec:       conf.Spec,
		Env:        conf.Env,
		Retry:      conf.Retry,
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

func (jobRun *JobRun) Exec() error {
	cmd := exec.Command(jobRun.Shell, "-c", jobRun.Command)
	cmd.Env = jobRun.Env
	cmd.Dir = jobRun.WorkingDir

	stderrFile, err := newLogFile(jobRun.StdErrorFilePath)
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer stderrFile.Close()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer stderr.Close()

	stderrScanner := bufio.NewScanner(stderr)

	stderrDone := make(chan string)

	go func() {
		if err := scannerToFile(stderrScanner, stderrFile); err != nil {
			tracerr.PrintSourceColor(err)
		}
		close(stderrDone)
	}()

	stdoutFile, err := newLogFile(jobRun.StdOutFilePath)
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
		if err := scannerToFile(stdoutScanner, stdoutFile); err != nil {
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
	return tracerr.Wrap(err)
}

func newLogFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
}

func scannerToFile(scanner *bufio.Scanner, file *os.File) error {
	for scanner.Scan() {
		text := scanner.Text()
		logLine := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), text)

		if _, err := file.Write([]byte(logLine)); err != nil {
			return tracerr.Wrap(err)
		}
	}

	return tracerr.Wrap(scanner.Err())
}
