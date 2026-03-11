package agent

import (
	"fmt"
	"os"
)

// Destroy removes an agent's directory from disk.
// The caller must ensure the container is stopped first.
func Destroy(home, name string) error {
	if !Exists(home, name) {
		return fmt.Errorf("agent %q does not exist", name)
	}
	dir := Dir(home, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("destroy: remove %s: %w", dir, err)
	}
	return nil
}
