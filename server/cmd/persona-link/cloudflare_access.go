package main

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/nwverse/persona/server/internal/link"
)

func cloudflareAccessCmd(e *env, args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	if args[0] == "--off" {
		return link.SetCloudflareAccess(e.configPath(), nil)
	}
	if args[0] != "--stdin" || e.stdinIsTerminal() {
		return errors.New(terminalText("link-cli.access_stdin"))
	}
	var a link.CloudflareAccess
	data, err := io.ReadAll(io.LimitReader(e.stdin, 8193))
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &a) != nil {
		return errors.New(terminalText("link-cli.access_invalid"))
	}
	return link.SetCloudflareAccess(e.configPath(), &a)
}
