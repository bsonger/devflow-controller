package config

import (
	"github.com/bsonger/devflow-common/model"
	"github.com/spf13/viper"
)

type Config struct {
	Log   *model.LogConfig   `mapstructure:"log"   yaml:"log"`
	Mongo *model.MongoConfig `mapstructure:"mongo" yaml:"mongo"`
	Otel  *model.OtelConfig  `mapstructure:"otel"  yaml:"otel"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	v.AddConfigPath("./config/")
	v.AddConfigPath("/etc/devflow-controller/config/")

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
