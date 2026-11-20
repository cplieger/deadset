//go:build unix

package documents_test

import "syscall"

func init() { makeFifo = syscall.Mkfifo }
