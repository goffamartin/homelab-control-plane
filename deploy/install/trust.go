// Package install pins the release trust anchor; downloads cannot replace it.
package install

import _ "embed"

//go:embed release.pub
var ReleasePublicKey []byte
