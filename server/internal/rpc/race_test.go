//go:build race

package rpc

func init() { raceEnabled = true }
