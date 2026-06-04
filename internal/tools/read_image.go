package tools

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif" // register decoder for image.Decode
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode-only; resized webp re-encodes as png
)

// maxImageDim caps either image dimension before downscaling kicks in.
const maxImageDim = 2000

// sniffImageMime detects supported image formats by magic bytes; returns ""
// for anything else.
func sniffImageMime(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// resizeImageIfNeeded returns the original bytes when the image fits inside
// maxImageDim²; otherwise it decodes, scales to fit (aspect preserved), and
// re-encodes — jpeg stays jpeg, everything else becomes png (gif loses
// animation; webp has no stdlib encoder).
func resizeImageIfNeeded(data []byte, mime string) (out []byte, outMime string, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	if cfg.Width <= maxImageDim && cfg.Height <= maxImageDim {
		return data, mime, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	w, h := cfg.Width, cfg.Height
	scale := float64(maxImageDim) / float64(max(w, h))
	dst := image.NewRGBA(image.Rect(0, 0, int(float64(w)*scale), int(float64(h)*scale)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if mime == "image/jpeg" {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80})
		outMime = "image/jpeg"
	} else {
		err = png.Encode(&buf, dst)
		outMime = "image/png"
	}
	if err != nil {
		return nil, "", err
	}
	return buf.Bytes(), outMime, nil
}

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
