package esctest

import (
	"bytes"

	"dm/internal/vt"
)

// vtReport asks the report VT to answer one sequence and returns its reply.
// The tested terminal keeps the grid; the report VT answers what the new VT
// now owns. Feeding the same bytes to both keeps their cursors together.
func (h *harness) vtReport(raw []byte) []byte {
	var buf bytes.Buffer
	vt.ReportWriter = &buf
	h.reports.Write(raw)
	vt.ReportWriter = nil
	return buf.Bytes()
}
