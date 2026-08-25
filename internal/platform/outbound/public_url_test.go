package outbound

import "testing"

func TestValidatePublicHTTPURL(t *testing.T) {
	for _, value := range []string{"https://source.example/api", "http://api.example.com:8080/vod"} {
		if err := ValidatePublicHTTPURL(value); err != nil {
			t.Fatalf("public URL %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"file:///etc/passwd", "http://localhost/admin", "http://service.internal/admin", "http://127.0.0.1/admin",
		"http://169.254.169.254/latest/meta-data", "http://10.0.0.1/", "http://[::1]/", "https://user:secret@source.example/api",
	} {
		if err := ValidatePublicHTTPURL(value); err == nil {
			t.Fatalf("unsafe URL %q accepted", value)
		}
	}
}
