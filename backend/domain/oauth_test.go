package domain

import "testing"

func TestValidRedirectURI(t *testing.T) {
	for _, tc := range []struct {
		uri   string
		valid bool
	}{
		{"https://client.example/callback", true},
		{"HTTPS://CLIENT.example:443/Callback?key=A", true},
		{"https://client.example/cb?next=%2Flogs%23section", true},
		{"https://client.example:65535/cb", true},
		{"http://localhost:54321/cb", true},
		{"http://LOCALHOST:54321/cb", true},
		{"http://127.0.0.1:54321/cb", true},
		{"http://127.0.0.2/cb", true},
		{"http://[::1]:54321/cb", true},
		{"http://[0:0:0:0:0:0:0:1]/cb", true},
		{"https://[2001:db8::1]/cb", true},
		{"", false},
		{"/callback", false},
		{"//client.example/cb", false},
		{"https:callback", false},
		{"https:/callback", false},
		{"https:///callback", false},
		{"https://", false},
		{"https://:443/cb", false},
		{"https://client.example/cb#fragment", false},
		{"https://client.example/cb#", false},
		{"https://user:pass@client.example/cb", false},
		{"https://@client.example/cb", false},
		{"https://client.example:/cb", false},
		{"https://client.example:0/cb", false},
		{"https://client.example:65536/cb", false},
		{"https://client.example:abc/cb", false},
		{"https://[not-an-ip]/cb", false},
		{"https://[::1%25eth0]/cb", false},
		{"https://::1/cb", false},
		{"https://client.example\\attacker.test/cb", false},
		{"https://client.example/has space", false},
		{"https://client.example/%zz", false},
		{"https://cliënt.example/cb", false},
		{"https://xn--clint-esa.example/cb", true},
		{"http://client.example/cb", false},
		{"http://localhost.attacker.test/cb", false},
		{"http://127.0.0.1.attacker.test/cb", false},
		{"http://[::2]/cb", false},
		{"http://[::ffff:127.0.0.1]/cb", false},
		{"javascript:alert(1)", false},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			if got := ValidRedirectURI(tc.uri); got != tc.valid {
				t.Fatalf("ValidRedirectURI(%q) = %v, want %v", tc.uri, got, tc.valid)
			}
		})
	}
}

func TestSameCanonicalURL(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"https://example.com", "HTTPS://EXAMPLE.COM/", true},
		{"https://example.com", "https://example.com:443", true},
		{"http://localhost", "http://LOCALHOST:080/", true},
		{"http://[::1]:4000", "http://[0:0:0:0:0:0:0:1]:4000/", true},
		{"https://EXAMPLE.com/Resource?key=A", "https://example.com/Resource?key=A", true},
		{"https://example.com/Resource", "https://example.com/resource", false},
		{"https://example.com/?key=A", "https://example.com/?key=a", false},
		{"https://example.com/?Key=a", "https://example.com/?key=a", false},
		{"https://example.com/path", "https://example.com/path/", false},
		{"https://example.com", "https://example.com///", false},
		{"https://example.com/a%2Fb", "https://example.com/a/b", false},
		{"https://example.com/%41", "https://example.com/A", false},
		{"https://example.com", "https://example.com?", false},
		{"https://example.com", "https://other.example.com", false},
		{"https://example.com", "http://example.com", false},
		{"http://localhost:4000", "http://localhost:4001", false},
		{"https://example.com/#", "https://example.com/#", false},
		{"https://user@example.com", "https://user@example.com", false},
		{"https:callback", "https:callback", false},
		{"", "", false},
	} {
		if got := SameCanonicalURL(tc.a, tc.b); got != tc.same {
			t.Errorf("SameCanonicalURL(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
		if got := SameCanonicalURL(tc.b, tc.a); got != tc.same {
			t.Errorf("reverse comparison of %q and %q = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func TestRedirectURIRegisteredUsesExactStrings(t *testing.T) {
	registered := []string{"https://example.com/Callback?key=A"}
	for _, uri := range []string{
		"https://EXAMPLE.com/Callback?key=A",
		"https://example.com:443/Callback?key=A",
		"https://example.com/callback?key=A",
		"https://example.com/Callback?key=a",
	} {
		if RedirectURIRegistered(registered, uri) {
			t.Errorf("callback %q must not match %q", uri, registered[0])
		}
	}
	if !RedirectURIRegistered(registered, registered[0]) {
		t.Fatal("the exact registered callback must match")
	}
}
