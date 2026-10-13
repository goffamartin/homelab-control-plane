//go:build linux && amd64

package setup

// x86-64 Linux UAPI __NR_renameat2; verified against asm/unistd_64.h.
const renameat2Number = 316
const transactionArchitectureSupported = true
