package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/image/webp"
)

func gifFixture(t *testing.T, count int) []byte {
	t.Helper()
	palette := color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}, color.RGBA{G: 255, A: 255}}
	g := &gif.GIF{Config: image.Config{Width: 4, Height: 4, ColorModel: palette}}
	for i := 0; i < count; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
		for j := range frame.Pix {
			frame.Pix[j] = byte(i%3 + 1)
		}
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, 10)
	}
	var data bytes.Buffer
	if err := gif.EncodeAll(&data, g); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestGIFPreviewAndOriginal(t *testing.T) {
	data := gifFixture(t, 12)
	preview, mime, note, err := ImagePreview(data, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := webp.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/webp" || decoded.Bounds().Dx() != 12 || decoded.Bounds().Dy() != 12 || !strings.Contains(note, "第12帧") || strings.Count(note, "毫秒") != 9 {
		t.Fatalf("bad preview %s %v %s", mime, decoded.Bounds(), note)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	mgr := New(Options{Dir: t.TempDir()})
	ref, err := mgr.Ensure(context.Background(), Source{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(ref.Path)
	if err != nil || !bytes.Equal(stored, data) || ref.Mime != "image/gif" {
		t.Fatalf("original changed: %v %s", err, ref.Mime)
	}
	_, _, limited, err := ImagePreview(gifFixture(t, 257), "image/gif")
	if err != nil || !strings.Contains(limited, "仅提供首帧") {
		t.Fatalf("limit: %s %v", limited, err)
	}
	if _, _, _, err := ImagePreview(data[:len(data)-1], "image/gif"); err == nil {
		t.Fatal("accepted truncated animation")
	}
}

func TestGIFDisposal(t *testing.T) {
	palette := color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}, color.RGBA{G: 255, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	for i := range first.Pix {
		first.Pix[i] = 1
	}
	second := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	second.Pix[0] = 2
	third := image.NewPaletted(image.Rect(1, 1, 2, 2), palette)
	third.Pix[0] = 3
	for _, disposal := range []byte{gif.DisposalPrevious, gif.DisposalBackground} {
		g := &gif.GIF{Config: image.Config{Width: 2, Height: 2, ColorModel: palette}, Image: []*image.Paletted{first, second, third}, Delay: []int{1, 1, 1}, Disposal: []byte{0, disposal, 0}}
		sheet, _ := gifContactSheet(g)
		if got := color.RGBAModel.Convert(sheet.At(2, 0)).(color.RGBA); got.B != 255 {
			t.Fatalf("local update missing: %v", got)
		}
		got := color.RGBAModel.Convert(sheet.At(0, 2)).(color.RGBA)
		if disposal == gif.DisposalPrevious && got.R != 255 {
			t.Fatalf("previous not restored: %v", got)
		}
		if disposal == gif.DisposalBackground && (got.R != 255 || got.G != 255 || got.B != 255) {
			t.Fatalf("background not cleared: %v", got)
		}
	}
}

func TestForwardFileDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("material")) }))
	defer server.Close()
	dir := t.TempDir()
	size := int64(8)
	hash, actual, err := CacheForwardFile(context.Background(), dir, server.URL, &size)
	if err != nil || actual != size {
		t.Fatalf("save: %d %v", actual, err)
	}
	path, _ := ForwardFilePath(dir, hash)
	if data, err := os.ReadFile(path); err != nil || string(data) != "material" {
		t.Fatalf("wrong bytes: %q %v", data, err)
	}
	size = 9
	if _, _, err := CacheForwardFile(context.Background(), dir, server.URL, &size); err == nil {
		t.Fatal("accepted short file")
	}
	if _, err := ForwardFilePath(dir, "../escape"); err == nil {
		t.Fatal("accepted invalid cache hash")
	}
}
