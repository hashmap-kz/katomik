package resolve

import "os"

func ReadFileContent(filename string) ([]byte, error) {
	if IsURL(filename) {
		return ReadRemoteFileContent(filename)
	}
	return os.ReadFile(filename)
}
