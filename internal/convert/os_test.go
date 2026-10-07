package convert

import "os"

func osWrite(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func osStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
