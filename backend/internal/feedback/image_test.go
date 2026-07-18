package feedback

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeImagesPreservesHistoricalDataURLAndFilenameContract(t *testing.T) {
	t.Parallel()

	content := []byte("image-content")
	encoded := base64.StdEncoding.EncodeToString(content)
	images, err := decodeImages([]ImageInput{{
		Name:    "  截图 alpha  ",
		DataURL: " \ndata:image/VND.ASCENDANY+PNG;base64," + encoded[:4] + " \n\t" + encoded[4:] + "  ",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].Sequence != 1 || images[0].Filename != "alpha.vnd.ascendany" ||
		images[0].MediaType != "image/vnd.ascendany+png" || string(images[0].Content) != string(content) ||
		len(images[0].SHA256) != 64 {
		t.Fatalf("decoded images=%#v", images)
	}
}

func TestDecodeImagesUsesHistoricalDefaultAndExistingExtension(t *testing.T) {
	t.Parallel()

	encoded := base64.StdEncoding.EncodeToString([]byte("png"))
	images, err := decodeImages([]ImageInput{
		{Name: "...", DataURL: "data:image/png;base64," + encoded},
		{Name: "SCREEN.JPG", DataURL: "data:image/jpeg;base64," + encoded},
	})
	if err != nil {
		t.Fatal(err)
	}
	if images[0].Filename != "screenshot_1.png" || images[1].Filename != "SCREEN.JPG.jpeg" {
		t.Fatalf("filenames=%q/%q", images[0].Filename, images[1].Filename)
	}
}

func TestDecodeImagesReturnsStableImageFailures(t *testing.T) {
	t.Parallel()

	valid := ImageInput{Name: "screen.png", DataURL: "data:image/png;base64,eA=="}
	tooMany := make([]ImageInput, MaxImages+1)
	for index := range tooMany {
		tooMany[index] = valid
	}
	if _, err := decodeImages(tooMany); CodeOf(err) != ErrorTooManyImages {
		t.Fatalf("too many error=%v code=%q", err, CodeOf(err))
	}

	for name, image := range map[string]ImageInput{
		"prefix":            {Name: "screen.png", DataURL: "DATA:image/png;base64,eA=="},
		"non image":         {Name: "screen.png", DataURL: "data:text/plain;base64,eA=="},
		"URL encoding":      {Name: "screen.png", DataURL: "data:image/png,eA=="},
		"invalid alphabet":  {Name: "screen.png", DataURL: "data:image/png;base64,eA-_"},
		"noncanonical bits": {Name: "screen.png", DataURL: "data:image/png;base64,eh=="},
		"empty":             {Name: "screen.png", DataURL: "data:image/png;base64,"},
		"long name":         {Name: strings.Repeat("界", MaxImageNameRunes+1), DataURL: valid.DataURL},
	} {
		name, image := name, image
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeImages([]ImageInput{image}); CodeOf(err) != ErrorImageInvalid {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
		})
	}
}

func TestDecodeImageRejectsDecodedPayloadAboveEightMiB(t *testing.T) {
	content := make([]byte, MaxImageBytes+1)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(content)
	if len(dataURL) > MaxImageDataURLBytes {
		t.Fatalf("test data URL=%d exceeds historical item bound", len(dataURL))
	}
	if _, err := decodeImages([]ImageInput{{Name: "large.png", DataURL: dataURL}}); CodeOf(err) != ErrorImageTooLarge {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
}
