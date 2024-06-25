package serve

import (
	"cronrunner/pkg/conf"
	"cronrunner/pkg/models"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"
	"github.com/ztrue/tracerr"
)

func Invoke(configPath string, bindAddress string) error {
	conf, err := conf.LoadFromYAML[conf.Configuration](configPath)
	if conf.Shell == "" {
		conf.Shell = "/bin/bash"
	}

	if err != nil {
		return tracerr.Wrap(err)
	}

	if err := os.MkdirAll(conf.LogDir, os.ModePerm); err != nil {
		return tracerr.Wrap(err)
	}

	logBaseDir := conf.LogDir

	c := cron.New()

	var jobs []*models.Job

	for idx := range conf.Jobs {
		job := models.NewJob(&conf.Jobs[idx])
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

		if _, err := c.AddFunc(job.Spec, func() {
			jobRunTotal.Inc()

			if err := Run(logBaseDir, job); err != nil {
				jobRunFailed.Inc()
				job.JobRunCountFailed += 1
				tracerr.PrintSourceColor(err)
			} else {
				jobRunSucceeded.Inc()
				job.JobRunCountSucceeded += 1
			}
		}); err != nil {
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

func Run(logBaseDir string, job *models.Job) error {
	logPath := filepath.Join(logBaseDir, "log.txt")
	appLogFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer appLogFile.Close()

	var appLogFunc = func(format string, a ...any) error {
		_, err := appLogFile.Write([]byte(
			fmt.Sprintf(format, a...),
		))
		if err := appLogFile.Sync(); err != nil {
			return tracerr.Wrap(err)
		}
		return tracerr.Wrap(err)
	}

	if err := appLogFunc("%s\t%s\tSTART\n", time.Now(), job.Id); err != nil {
		return tracerr.Wrap(err)
	}

	jobLogDir := filepath.Join(logBaseDir, job.Id)

	if err := os.MkdirAll(jobLogDir, os.ModePerm); err != nil {
		return tracerr.Wrap(err)
	}

	stdoutFilePath := filepath.Join(jobLogDir, "stdout.txt")
	stderrFilePath := filepath.Join(jobLogDir, "stderr.txt")

	jobRun := models.NewJobRun(job, stdoutFilePath, stderrFilePath)
	job.LastRun = jobRun
	if err := jobRun.Run(); err != nil {
		return tracerr.Wrap(err)
	}

	if err := appLogFunc("%s\t%s\tLastRunStatus=%s\n", time.Now(), job.Id, models.SUCCEEDED); err != nil {
		return tracerr.Wrap(err)
	}
	return nil
}
