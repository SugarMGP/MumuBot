package version

import "strings"

// Build 是构建时注入的应用版本，开发构建使用 dev
var Build = "dev"

func String() string {
	value := strings.TrimSpace(Build)
	if value == "" {
		return "dev"
	}
	return value
}
