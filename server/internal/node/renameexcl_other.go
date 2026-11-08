//go:build !darwin && !linux

package node

// renameExcl renames from to to unless to exists (os.ErrExist then); here with a check just before.
func renameExcl(from, to string) error { return renameIfFree(from, to) }
