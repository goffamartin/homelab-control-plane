//go:build linux && arm64

package setup

import "syscall"

const renameat2Number = syscall.SYS_RENAMEAT2
const transactionArchitectureSupported = true
