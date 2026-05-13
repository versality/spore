package workerwatch

import "os"

func writeFileBytes(path string, body []byte) error {
	return os.WriteFile(path, body, 0o600)
}
