//go:build !linux && !darwin

package node

import "errors"

func updateProcessArgv(int) ([]string, error) {
	return nil, errors.New("authoritative process arguments unsupported on this platform")
}
