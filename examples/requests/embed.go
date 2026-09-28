// Package request embeds the walkthrough's requests for demo-cli.
package request

import "embed"

// Files contains the same JSON requests the integration suite exercises.
// Embedding them lets demo-cli run from any working directory.
//
//go:embed *.json
var Files embed.FS
