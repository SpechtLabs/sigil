// Package request embeds the walkthrough's requests for demo-cli. Each JSON
// file is the body of one demo-cli scenario, a deployment request or an
// access request. The README's curl examples post the same files, and the
// integration suite sends each one to the server and checks that it still
// answers the status the README shows.
package request

import "embed"

// Files contains the same JSON requests the integration suite exercises.
// Embedding them lets demo-cli run from any working directory.
//
//go:embed *.json
var Files embed.FS
