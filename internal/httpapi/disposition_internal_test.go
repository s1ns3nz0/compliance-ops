package httpapi

import "testing"

func TestContentDisposition(t *testing.T) {
	cases := map[string]string{
		"policy.pdf":                 `attachment; filename="policy.pdf"`,
		"../we;ird/report.pdf":       `attachment; filename="report.pdf"`,
		"":                           `attachment; filename="evidence"`,
		"   ":                        `attachment; filename="evidence"`,
		"..":                         `attachment; filename="evidence"`,
		"a\"b\\c\r\nd.txt":           `attachment; filename="c__d.txt"`,
		"C:\\Users\\x\\rapport.docx": `attachment; filename="rapport.docx"`,
		"audit-report.pdf":           `attachment; filename="audit-report.pdf"`,
		"résumé":                     `attachment; filename="rsum"; filename*=UTF-8''r%C3%A9sum%C3%A9`,
	}
	for in, want := range cases {
		if got := contentDisposition(in); got != want {
			t.Errorf("contentDisposition(%q) = %q, want %q", in, got, want)
		}
	}
}
