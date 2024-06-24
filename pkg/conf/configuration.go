package conf

import (
	"os"

	"gopkg.in/yaml.v3"
)

type JobConfig struct {
	Id      string   `yaml:"id"`
	Command string   `yaml:"command"`
	Spec    string   `yaml:"spec"`
	Args    []string `yaml:"args"`
}

type Configuration struct {
	Jobs   []JobConfig `yaml:"jobs"`
	LogDir string      `yaml:"log_dir"`
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
