package node

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func updateProcessArgv(pid int) ([]string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != 0 {
		return nil, fmt.Errorf("incomplete process arguments")
	}
	return strings.Split(string(data[:len(data)-1]), "\x00"), nil
}
