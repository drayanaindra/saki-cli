package usecase

import "testing"

type fakeOSAScript struct {
	code   int
	stdout string
	stderr string
}

func (f fakeOSAScript) ChooseFolder() (int, string, string) { return f.code, f.stdout, f.stderr }

func TestPickFolder_nonDarwin501(t *testing.T) {
	status, body := NewPickFolderService(fakeOSAScript{}, "linux").Pick()
	if status != 501 {
		t.Fatalf("non-darwin must be 501, got %d", status)
	}
	if body["error"] == nil {
		t.Fatalf("501 body must carry an error, got %+v", body)
	}
}

func TestPickFolder_darwinPaths(t *testing.T) {
	cases := []struct {
		name       string
		runner     fakeOSAScript
		wantStatus int
		wantKey    string
	}{
		{"selection → 200 path", fakeOSAScript{code: 0, stdout: "/Users/me/repo\n"}, 200, "path"},
		{"cancel → 200 canceled", fakeOSAScript{code: 1, stderr: "User canceled. (-128)"}, 200, "canceled"},
		{"error → 500 error", fakeOSAScript{code: 1, stderr: "boom"}, 500, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := NewPickFolderService(tc.runner, "darwin").Pick()
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if _, ok := body[tc.wantKey]; !ok {
				t.Fatalf("body must carry key %q, got %+v", tc.wantKey, body)
			}
		})
	}
}
