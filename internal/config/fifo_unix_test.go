//go:build unix

package config_test

import "syscall"

func init() { makeFifo = syscall.Mkfifo }
