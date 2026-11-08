package osproc

// SealInherited does nothing on Windows: Go starts a child with only the handles it lists for it, and there is no
// socket activation.
func SealInherited() {}
