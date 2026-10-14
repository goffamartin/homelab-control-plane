//go:build !linux

package doctor

import "time"

func (NativeHost) DiagnoseUser(Options, time.Time) (Report, error) { return Report{}, errUser }
