package feedback

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	imageDataURLPrefix    = regexp.MustCompile(`^data:(image/[A-Za-z0-9.+-]+);base64,`)
	imageMediaTypePattern = regexp.MustCompile(`^image/[a-z0-9.+-]+$`)
)

type decodedImage struct {
	Sequence  int16
	Filename  string
	MediaType string
	SHA256    string
	Content   []byte
}

func decodeImages(images []ImageInput) ([]decodedImage, error) {
	if len(images) > MaxImages {
		return nil, feedbackError(ErrorTooManyImages, true, "decode feedback images", errors.New("feedback image count exceeds eight"))
	}
	decoded := make([]decodedImage, len(images))
	for index, image := range images {
		value, err := decodeImage(image, index+1)
		if err != nil {
			return nil, err
		}
		decoded[index] = value
	}
	return decoded, nil
}

func decodeImage(input ImageInput, sequence int) (decodedImage, error) {
	if sequence < 1 || sequence > MaxImages || !utf8.ValidString(input.Name) ||
		utf8.RuneCountInString(input.Name) > MaxImageNameRunes || !utf8.ValidString(input.DataURL) ||
		len(input.DataURL) == 0 || len(input.DataURL) > MaxImageDataURLBytes {
		return decodedImage{}, imageInvalid("image name or data URL violates its bounded UTF-8 contract")
	}
	dataURL := strings.TrimSpace(input.DataURL)
	prefix := imageDataURLPrefix.FindStringSubmatchIndex(dataURL)
	if prefix == nil || prefix[0] != 0 {
		return decodedImage{}, imageInvalid("image data URL prefix is invalid")
	}
	mediaType := strings.ToLower(dataURL[prefix[2]:prefix[3]])
	encodedWithWhitespace := dataURL[prefix[1]:]
	if encodedWithWhitespace == "" {
		return decodedImage{}, imageInvalid("image data URL payload is empty")
	}
	var encoded strings.Builder
	encoded.Grow(len(encodedWithWhitespace))
	for _, value := range encodedWithWhitespace {
		switch {
		case unicode.IsSpace(value):
			continue
		case value <= unicode.MaxASCII && isBase64Character(byte(value)):
			encoded.WriteByte(byte(value))
		default:
			return decodedImage{}, imageInvalid("image data URL contains a non-base64 character")
		}
	}
	if encoded.Len() == 0 {
		return decodedImage{}, imageInvalid("image data URL payload is empty")
	}
	content, err := base64.StdEncoding.Strict().DecodeString(encoded.String())
	if err != nil || len(content) == 0 {
		return decodedImage{}, imageInvalid("image base64 payload is invalid")
	}
	if len(content) > MaxImageBytes {
		return decodedImage{}, feedbackError(ErrorImageTooLarge, true, "decode feedback image", errors.New("decoded feedback image exceeds eight MiB"))
	}
	filename, err := safeImageFilename(input.Name, sequence, mediaType)
	if err != nil {
		return decodedImage{}, err
	}
	digest := sha256.Sum256(content)
	return decodedImage{
		Sequence:  int16(sequence),
		Filename:  filename,
		MediaType: mediaType,
		SHA256:    hex.EncodeToString(digest[:]),
		Content:   content,
	}, nil
}

func safeImageFilename(name string, sequence int, mediaType string) (string, error) {
	cleaned := strings.TrimSpace(name)
	var normalized strings.Builder
	lastInvalid := false
	for _, value := range cleaned {
		valid := value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' || value == '.' || value == '_' || value == '-'
		if valid {
			normalized.WriteRune(value)
			lastInvalid = false
			continue
		}
		if !lastInvalid {
			normalized.WriteByte('_')
			lastInvalid = true
		}
	}
	cleaned = normalized.String()
	if len(cleaned) > 120 {
		cleaned = cleaned[:120]
	}
	cleaned = strings.Trim(cleaned, "._")
	if cleaned == "" {
		cleaned = "screenshot_" + decimalSequence(sequence)
	}
	subtype := strings.TrimPrefix(mediaType, "image/")
	if before, _, found := strings.Cut(subtype, "+"); found {
		subtype = before
	}
	if subtype == "" {
		return "", imageInvalid("image media subtype is empty")
	}
	extension := "." + strings.ToLower(subtype)
	if !strings.HasSuffix(strings.ToLower(cleaned), extension) {
		cleaned += extension
	}
	if len(cleaned) == 0 || len(cleaned) > MaxAttachmentFilenameBytes {
		return "", imageInvalid("normalized image filename exceeds its byte limit")
	}
	return cleaned, nil
}

func isBase64Character(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9' || value == '+' || value == '/' || value == '='
}

func decimalSequence(sequence int) string {
	return strconv.Itoa(sequence)
}

func imageDataURLMediaType(value string) bool { return imageMediaTypePattern.MatchString(value) }

func imageInvalid(detail string) error {
	return feedbackError(ErrorImageInvalid, true, "decode feedback image", errors.New(detail))
}
