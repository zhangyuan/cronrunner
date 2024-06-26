package conf

import (
	"os"

	"gopkg.in/yaml.v3"
)

type JobConfig struct {
	Id         string   `yaml:"id"`
	Command    string   `yaml:"command"`
	WorkingDir string   `yaml:"working_dir"`
	Spec       string   `yaml:"spec"`
	Env        []string `yaml:"env"`
	Retry      int      `yaml:"retry"`
}

type Configuration struct {
	LogDir string      `yaml:"log_dir"`
	Shell  string      `yaml:"shell"`
	Jobs   []JobConfig `yaml:"jobs"`
}

func LoadFromYAML[T any](path string) (*T, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t T
	if err := yaml.Unmarshal(bytes, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
