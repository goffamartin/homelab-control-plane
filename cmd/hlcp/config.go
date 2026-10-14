package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/cli"
	"hlcp/internal/localapi"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type clientConfig = cli.ClientConfig

func clientConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil || !filepath.IsAbs(dir) {
		return "", errors.New("cannot locate an absolute user configuration directory")
	}
	return filepath.Join(dir, "hlcp", "cli.json"), nil
}

func loadClientConfig(path string) (clientConfig, error) {
	var config clientConfig
	invalid := errors.New("invalid client configuration (expected private, version-1 JSON with loopback API and token file)")
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return config, invalid
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return config, invalid
	}
	config, err = cli.ParseClientConfig(data, path)
	if err != nil {
		return clientConfig{}, invalid
	}
	return config, nil
}

func applyClientConfig(request *cli.Request) error {
	// Fully explicit transport remains independent of a user's saved profile.
	if request.ConfigFile == "" && request.API != "" && request.APITokenFile != "" {
		return nil
	}
	path := request.ConfigFile
	if path == "" {
		var err error
		path, err = clientConfigPath()
		if err != nil {
			return err
		}
	}
	config, err := loadClientConfig(path)
	if errors.Is(err, os.ErrNotExist) && request.ConfigFile == "" {
		return nil
	}
	if err != nil {
		return errors.New("cannot load client configuration; inspect it or use explicit --api and --api-token-file")
	}
	if request.API == "" {
		request.API = config.API
	}
	if request.APITokenFile == "" {
		request.APITokenFile = config.TokenFile
	}
	return nil
}

func configure(args []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("hlcp configure", flag.ContinueOnError)
	flags.SetOutput(output)
	api := flags.String("api", defaultAPI, "loopback API URL (SSH tunnels supported)")
	tokenFile := flags.String("api-token-file", "", "existing private token file; save its path, not its contents")
	stdin := flags.Bool("token-stdin", false, "import authorized token from stdin into a new private file; never use token arguments")
	path := flags.String("config", "", "new configuration file; default: user hlcp/cli.json")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: hlcp configure [--api URL] (--api-token-file FILE | --token-stdin) [--config FILE]\nRun as the intended CLI user. Existing configuration is never overwritten.\nThis stores an already authorized credential, not a new identity or permission grant.\nUnix files are private; on Windows protect the directory with your user ACL.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || (*stdin == (*tokenFile != "")) {
		return errors.New("choose exactly one of --api-token-file or --token-stdin; no positional arguments")
	}
	if err := localapi.ValidateURL(*api); err != nil {
		return err
	}
	if *path == "" {
		var err error
		*path, err = clientConfigPath()
		if err != nil {
			return err
		}
	}
	configPath, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(configPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("configuration already exists or cannot be inspected; refusing overwrite")
	}
	// Validate credential input before creating any files/directories.
	var token []byte
	if *stdin {
		token, err = io.ReadAll(io.LimitReader(input, 259))
		value := strings.TrimSuffix(strings.TrimSuffix(string(token), "\n"), "\r")
		if err != nil || len(token) > 258 || len(value) < 32 || len(value) > 256 {
			return errors.New("invalid token input")
		}
		for _, ch := range value {
			if ch < 0x21 || ch > 0x7e {
				return errors.New("invalid token input")
			}
		}
		token = []byte(value + "\n")
	} else {
		*tokenFile, err = filepath.Abs(*tokenFile)
		if err != nil {
			return err
		}
		if _, err := localapi.LoadToken(*tokenFile); err != nil {
			return err
		}
	}
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("configuration directory must be private and not a symlink; existing permissions were not changed")
	}
	createdToken := ""
	if *stdin {
		file, err := os.CreateTemp(dir, "cli-*.token")
		if err != nil {
			return err
		}
		createdToken = file.Name()
		_, writeErr := file.Write(token)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(createdToken)
			return errors.New("cannot save private token")
		}
		*tokenFile = createdToken
	}
	data, _ := json.MarshalIndent(clientConfig{Version: 1, API: *api, TokenFile: *tokenFile}, "", "  ")
	file, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if createdToken != "" {
			os.Remove(createdToken)
		}
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(configPath)
		if createdToken != "" {
			os.Remove(createdToken)
		}
		return errors.New("cannot save configuration")
	}
	fmt.Fprintln(output, "Client configured. Try: hlcp -n")
	return nil
}
