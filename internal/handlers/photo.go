package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"bitebox/internal/imageproc"
)

// productPhotoDir is where uploaded (as opposed to externally-URL-linked)
// product photos live, under static/ so the existing file server (see
// neuteredFileSystem in main.go) already serves them with no new route.
const productPhotoDir = "static/uploads/products"

// fullSuffix/thumbSuffix name the two variants imageproc.Process produces
// for a given upload — both share the same content hash, so a product's
// image_url (which always stores the full variant's URL, see
// thumbURLFor) and its thumbnail differ only by this suffix.
const (
	fullSuffix  = "_full.jpg"
	thumbSuffix = "_thumb.jpg"
)

// writeProductPhoto saves an already-processed (resized, re-encoded, hashed)
// upload for productID and returns the URL to store on the product's
// image_url column — always the *full* variant's URL; see thumbURLFor for
// how a template gets the thumbnail from that one stored value.
//
// Both variants land under a per-product subdirectory
// (.../products/<id>/<hash>_full.jpg, ..._thumb.jpg) named by content hash
// rather than the old product-ID-plus-timestamp scheme: identical bytes
// (a guest re-uploading the exact same photo) land on the exact same URL
// instead of manufacturing a new one, and — combined with main.go's
// staticCacheHeaders, which caches anything under uploads/products/
// immutably forever — a hash-named URL is safe to cache that aggressively
// precisely because its content can never change under a guest's nose.
func writeProductPhoto(productID int, result imageproc.Result) (url string, err error) {
	dir := filepath.Join(productPhotoDir, strconv.Itoa(productID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	fullPath := filepath.Join(dir, result.Hash+fullSuffix)
	thumbPath := filepath.Join(dir, result.Hash+thumbSuffix)
	if err := os.WriteFile(fullPath, result.Full.JPEG, 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(thumbPath, result.Thumb.JPEG, 0644); err != nil {
		return "", err
	}
	return "/" + dir + "/" + result.Hash + fullSuffix, nil
}

// thumbURLFor derives a product's thumbnail URL from its stored (full)
// image_url — the schema keeps just the one ImageURL column (see
// models.Product), so the thumbnail is derived by filename convention
// rather than adding a second DB column/migration for it, the smallest
// change that gets a real thumbnail URL to the templates that need one
// (see productView.ThumbURL in admin.go). Anything that isn't one of our
// own "<hash>_full.jpg" uploads — blank, or an admin-pasted external
// http(s) URL, which never went through imageproc at all and so has no
// separate thumbnail file sitting next to it — just returns unchanged:
// the "thumbnail" for those is the same image, at whatever size the
// browser fetches it at.
func thumbURLFor(imageURL string) string {
	if strings.HasSuffix(imageURL, fullSuffix) {
		return strings.TrimSuffix(imageURL, fullSuffix) + thumbSuffix
	}
	return imageURL
}

// deleteLocalPhotoIfAny removes a previously-uploaded photo's files when
// it's being replaced or cleared — a no-op for an external image_url
// (nothing of ours to clean up) or an empty one. Best-effort: a failed
// delete just leaves an orphaned file on disk, not a broken product, so
// the error is swallowed rather than surfaced to the admin mid otherwise-
// successful edit. Removes both variants, not just the full one passed in.
func deleteLocalPhotoIfAny(url string) {
	prefix := "/" + productPhotoDir + "/"
	if !strings.HasPrefix(url, prefix) {
		return
	}
	os.Remove(strings.TrimPrefix(url, "/"))
	if strings.HasSuffix(url, fullSuffix) {
		thumbPath := strings.TrimSuffix(strings.TrimPrefix(url, "/"), fullSuffix) + thumbSuffix
		os.Remove(thumbPath)
	}
}

// photoUploadErrorMessage turns an imageproc error into the admin-facing
// text that actually shows up on the dashboard — imageproc's own error
// values are already written as complete, displayable sentences (see
// e.g. ErrHEICNotSupported), so most of them pass through as-is; this
// exists for the couple of errors worth a slightly different phrasing in
// context and the fallback for anything unrecognized.
func photoUploadErrorMessage(err error) string {
	switch err {
	case imageproc.ErrTooLarge:
		return "That photo is too large (max 20MB)."
	case imageproc.ErrTooManyPixels:
		return "That photo's resolution is too large to process."
	case imageproc.ErrNotAnImage:
		return "Please upload a JPEG, PNG, or GIF photo."
	case imageproc.ErrHEICNotSupported:
		return imageproc.ErrHEICNotSupported.Error()
	default:
		return "Failed to process that photo — please try again."
	}
}

// allowMultipartUpload caps the raw request body (http.MaxBytesReader,
// enforced on every subsequent read of r.Body) and parses it as
// multipart/form-data. A request with no file part at all (a plain
// urlencoded POST, e.g. from something other than the photo-upload forms)
// still parses fine here — http.ErrNotMultipart only fires for a
// Content-Type that isn't multipart at all, which every caller of this
// helper's forms always send (see hx-encoding="multipart/form-data" in the
// templates), so treating that one error as non-fatal is just a safety net,
// not the expected path.
func allowMultipartUpload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, imageproc.MaxUploadBytes)
	if err := r.ParseMultipartForm(imageproc.MaxUploadBytes); err != nil && err != http.ErrNotMultipart {
		return err
	}
	return nil
}
