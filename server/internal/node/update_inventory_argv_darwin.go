package node

import "golang.org/x/sys/unix"

func updateProcessArgv(pid int) ([]string, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	return updateDarwinArgv(data)
}
