package session

import "os"

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func removeFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
