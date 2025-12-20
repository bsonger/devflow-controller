package config

import "fmt"

func (c *Config) Validate() error {
	if c.Log == nil {
		return fmt.Errorf("log config is required")
	}
	if c.Mongo == nil {
		return fmt.Errorf("mongo config is required")
	}
	return nil
}
