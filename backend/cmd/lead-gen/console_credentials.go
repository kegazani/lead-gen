package main

import (
	"errors"
	"strings"
)

func resolveConsolePassword(username, secretFile string, lookupEnv func(string) (string, bool)) (string, error) {
	if _, supplied := lookupEnv("CONSOLE_PASSWORD"); supplied {
		return "", errors.New("lead-gen: CONSOLE_PASSWORD запрещён; используйте внешний console password file")
	}
	username = strings.TrimSpace(username)
	secretFile = strings.TrimSpace(secretFile)
	if username == "" {
		if secretFile != "" {
			return "", errors.New("lead-gen: console password file задан без console user")
		}
		return "", nil
	}
	if secretFile == "" {
		return "", errors.New("lead-gen: console user требует внешний console password file")
	}
	password, err := readSecretFile(secretFile)
	if err != nil || len(password) == 0 {
		return "", errors.New("lead-gen: console password file отсутствует, пуст или имеет небезопасные права")
	}
	return password, nil
}
