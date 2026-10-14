package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

type UserHost interface {
	DiagnoseUser(Options, time.Time) (Report, error)
}

var errUser = errors.New("selected diagnostic account unavailable")

const maximumUserReport = 256 * 1024

func decodeUserReport(data []byte, exit int) (Report, error) {
	if len(data) > maximumUserReport || (exit != 0 && exit != 1) {
		return Report{}, errUser
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || !report.ReadOnly || len(report.Checks) == 0 || len(report.Checks) > 4096 {
		return Report{}, errUser
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) || report.Failed() != (exit == 1) {
		return Report{}, errUser
	}
	for _, check := range report.Checks {
		if len(check.Code) == 0 || len(check.Code) > 128 || len(check.Message) > 1024 {
			return Report{}, errUser
		}
		switch check.State {
		case "ok", "warn", "fail", "skip":
		default:
			return Report{}, errUser
		}
		for _, char := range check.Code {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_') {
				return Report{}, errUser
			}
		}
	}
	return report, nil
}

func userArguments(options Options) []string {
	args := []string{"doctor", "--role", "agent", "--scope", "user", "--config", options.Config, "--json"}
	if options.Unit != "" {
		args = append(args, "--unit", options.Unit)
	}
	if options.SetupDirectory != "" {
		args = append(args, "--setup-dir", options.SetupDirectory)
	}
	if options.HostChecks {
		args = append(args, "--host-checks")
	}
	if options.Probe {
		args = append(args, "--probe")
	}
	// Do not forward --user, argv/env/config secrets, HOME or arbitrary flags.
	return args
}
