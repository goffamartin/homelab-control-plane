package main

import (
	"fmt"
	"hlcp/internal/cli"
	"os"
)

func main() {
	request, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Use --help for usage.")
		os.Exit(2)
	}
	if request.Help {
		printHelp()
		return
	}
	fmt.Printf("hlcp: %s requires a controller connection; use --help for navigation.\n", cli.Intent(request))
}

func printHelp() {
	fmt.Print(`HLCP — HomeLab Control Plane

Progressive navigation:
  hlcp -n
  hlcp -n debian-01 -r
  hlcp -n debian-01 -r example-catalog -a
  hlcp -n debian-01 -r example-catalog -a restart

Explicit forms:
  --nodes | --node <id>
  --resources | --resource <id>
  --actions | --action <name>

Options:
  -n, -r, -a          navigational selectors; value is optional
  -h, --help          show this help
  --dry-run           validate without sending a command
  --json              machine-readable output
`)
}
