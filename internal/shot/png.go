package shot

import (
	"bytes"
	"image/png"
)

// PNG encodes the raster as a PNG image.
func (r *Raster) PNG() ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, r.Image); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
