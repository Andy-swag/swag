// Swag — a quiet web console and super-agent for coding agents in herdr.
package main

import "github.com/Andy-swag/swag/internal/swag"

// version is set at release time (-ldflags "-X main.version=v0.1.0").
var version = "dev"

func main() { swag.Main(version) }
