package config

import (
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			d.Duration = 0
			return nil
		}
		if n, err := strconv.ParseInt(value.Value, 10, 64); err == nil {
			d.Duration = time.Duration(n)
			return nil
		}
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return fmt.Errorf("parse duration %q: %w", value.Value, err)
		}
		d.Duration = parsed
		return nil
	default:
		return fmt.Errorf("duration must be scalar, got yaml kind %d", value.Kind)
	}
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}
