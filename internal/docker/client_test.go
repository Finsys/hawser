package docker

import "testing"

func TestContainerAttachPath_EscapesID(t *testing.T) {
	cases := []struct {
		name        string
		apiVersion  string
		containerID string
		want        string
	}{
		{
			name:        "plain hex id",
			apiVersion:  "v1.43",
			containerID: "abc123def456",
			want:        "/v1.43/containers/abc123def456/attach?stream=1&stdin=1&stdout=1&stderr=1",
		},
		{
			name:        "crlf injection is neutralized",
			apiVersion:  "v1.43",
			containerID: "abc\r\nGET /evil",
			want:        "/v1.43/containers/abc%0D%0AGET%20%2Fevil/attach?stream=1&stdin=1&stdout=1&stderr=1",
		},
		{
			name:        "slash cannot escape the id segment",
			apiVersion:  "v1.43",
			containerID: "../../secret",
			want:        "/v1.43/containers/..%2F..%2Fsecret/attach?stream=1&stdin=1&stdout=1&stderr=1",
		},
		{
			name:        "query and fragment separators are escaped",
			apiVersion:  "v1.43",
			containerID: "id?foo=bar#frag",
			want:        "/v1.43/containers/id%3Ffoo=bar%23frag/attach?stream=1&stdin=1&stdout=1&stderr=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containerAttachPath(tc.apiVersion, tc.containerID)
			if got != tc.want {
				t.Fatalf("containerAttachPath(%q, %q)\n got: %s\nwant: %s", tc.apiVersion, tc.containerID, got, tc.want)
			}
		})
	}
}
