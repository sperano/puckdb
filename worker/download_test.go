package worker

// mockDownloader returns a Downloader that returns the given content and error.
func mockDownloader(content []byte, err error) Downloader {
	return func(url string) ([]byte, error) {
		return content, err
	}
}
