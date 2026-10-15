package main

import "testing"

func TestManagedArgumentsAcceptsOnlyKnownGlobalPrefix(t *testing.T) {
	for _, test := range []struct {
		args []string
		ok   bool
	}{{[]string{"pairing", "list"}, true}, {[]string{"--api-token-file", "/private/token", "pairing", "list"}, true}, {[]string{"--api=http://127.0.0.1:9444", "certificates"}, true}, {[]string{"-n", "pairing"}, false}, {[]string{"--unknown", "certificates"}, false}, {[]string{"--api", "pairing"}, false}} {
		_, ok := managedArguments(test.args)
		if ok != test.ok {
			t.Fatalf("%q: %t", test.args, ok)
		}
	}
}
