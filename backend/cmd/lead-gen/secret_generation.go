package main

import (
	"errors"
	"path/filepath"
	"strings"
)

func verifyConfiguredSecretGeneration(expected string, paths ...string) error {
	active := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			active = append(active, path)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return verifyStagedGeneration(expected, active...)
}

func verifyStagedGeneration(expected string, secretPaths ...string) error {
	active := make([]string, 0, len(secretPaths))
	for _, secretPath := range secretPaths {
		if secretPath != "" {
			active = append(active, secretPath)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if expected == "" {
		return errors.New("lead-gen: для staged secret files не задано ожидаемое поколение")
	}
	if !validSecretGeneration(expected) {
		return errors.New("lead-gen: отсутствует или некорректно задано поколение secret bundle")
	}
	for _, secretPath := range active {
		if filepath.Base(filepath.Dir(secretPath)) != expected {
			return errors.New("lead-gen: staged secret не соответствует ожидаемому поколению")
		}
		marker, err := readSecretFile(filepath.Join(filepath.Dir(secretPath), "generation"))
		if err != nil || strings.TrimSpace(marker) != expected {
			return errors.New("lead-gen: staged secret bundle отсутствует, неполон или устарел")
		}
	}
	return nil
}
