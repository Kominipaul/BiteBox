package handlers

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"bitebox/internal/db"

	"github.com/go-chi/chi/v5"
)

// buildMultipartProductForm assembles a multipart/form-data body for the
// product create/update endpoints, optionally attaching a synthetic JPEG
// under the "photo" field.
func buildMultipartProductForm(t *testing.T, fields map[string]string, photo []byte) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s): %v", k, err)
		}
	}
	if photo != nil {
		part, err := w.CreateFormFile("photo", "upload.jpg")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write(photo); err != nil {
			t.Fatalf("writing photo bytes: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	return body, w.FormDataContentType()
}

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("building test JPEG: %v", err)
	}
	return buf.Bytes()
}

func TestAdminCreateProductWithPhotoUpload(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	body, contentType := buildMultipartProductForm(t, map[string]string{
		"name":     "Test Photo Item",
		"price":    "9.50",
		"category": "Test Photo Category",
	}, testJPEG(t, 3000, 2000)) // oversized on purpose — must come out resized

	r := httptest.NewRequest(http.MethodPost, "/admin/products", body)
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	AdminCreateProduct(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	products, err := db.GetAllProducts()
	if err != nil {
		t.Fatalf("GetAllProducts: %v", err)
	}
	var imageURL string
	found := false
	for _, p := range products {
		if p.Name == "Test Photo Item" {
			imageURL = p.ImageURL
			found = true
			break
		}
	}
	if !found {
		t.Fatal("product was not created")
	}
	if imageURL == "" {
		t.Fatal("expected the uploaded photo's URL to be attached to the product, got empty ImageURL")
	}

	diskPath := imageURL[1:] // strip leading "/" — path is relative to the repo/working dir
	defer os.Remove(diskPath)
	info, err := os.Stat(diskPath)
	if err != nil {
		t.Fatalf("uploaded photo file not found on disk at %s: %v", diskPath, err)
	}
	if info.Size() == 0 {
		t.Error("uploaded photo file is empty")
	}

	decoded, err := jpeg.Decode(bytes.NewReader(mustReadFile(t, diskPath)))
	if err != nil {
		t.Fatalf("stored photo isn't a valid JPEG: %v", err)
	}
	b := decoded.Bounds()
	if b.Dx() > 1600 || b.Dy() > 1600 {
		t.Errorf("stored photo is %dx%d — the 3000x2000 upload should have been resized down to <=1600 on its longer edge", b.Dx(), b.Dy())
	}
}

// TestAdminCreateProductWritesAndDeletesBothPhotoVariants checks the part
// of the pipeline TestAdminCreateProductWithPhotoUpload doesn't: that a
// "full" upload also produces a sibling "_thumb.jpg" file next to it (see
// imageproc.Process's two variants and writeProductPhoto), that ImageURL
// itself is the full variant's URL, and that removing the photo cleans up
// both files, not just the one named on the product row.
func TestAdminCreateProductWritesAndDeletesBothPhotoVariants(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category 6", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	body, contentType := buildMultipartProductForm(t, map[string]string{
		"name": "Test Both Variants Item", "price": "7.00", "category": "Test Photo Category 6",
	}, testJPEG(t, 2000, 1500))

	r := httptest.NewRequest(http.MethodPost, "/admin/products", body)
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	AdminCreateProduct(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	products, _ := db.GetAllProducts()
	var id int
	var fullURL string
	for _, p := range products {
		if p.Name == "Test Both Variants Item" {
			id, fullURL = p.ID, p.ImageURL
			break
		}
	}
	if fullURL == "" {
		t.Fatal("product was not created with a photo")
	}
	if !strings.HasSuffix(fullURL, fullSuffix) {
		t.Fatalf("ImageURL = %q, want it to end in %q (the full variant)", fullURL, fullSuffix)
	}

	fullPath := fullURL[1:]
	thumbPath := strings.TrimSuffix(fullPath, fullSuffix) + thumbSuffix
	if _, err := os.Stat(fullPath); err != nil {
		t.Errorf("full variant not found on disk: %v", err)
	}
	if _, err := os.Stat(thumbPath); err != nil {
		t.Errorf("thumb variant not found on disk at %s: %v", thumbPath, err)
	}
	decodedThumb, err := jpeg.Decode(bytes.NewReader(mustReadFile(t, thumbPath)))
	if err != nil {
		t.Fatalf("thumb variant isn't a valid JPEG: %v", err)
	}
	if b := decodedThumb.Bounds(); b.Dx() > 640 || b.Dy() > 640 {
		t.Errorf("thumb variant is %dx%d, want both dimensions <= 640", b.Dx(), b.Dy())
	}

	// Now remove the photo — both files should go, not just the full one.
	body2, ct2 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Both Variants Item", "price": "7.00", "category": "Test Photo Category 6", "remove_photo": "1",
	}, nil)
	r2 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body2)
	r2.Header.Set("Content-Type", ct2)
	r2 = withChiURLParam(r2, "id", strconv.Itoa(id))
	AdminUpdateProduct(httptest.NewRecorder(), r2)

	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Errorf("full variant should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Errorf("thumb variant should also have been deleted, stat err = %v", err)
	}
}

// TestAdminCreateProductRejectsHEICWithClearMessage exercises the HEIC
// rejection path (see imageproc.ErrHEICNotSupported) through the real HTTP
// handler, not just imageproc.Process directly — checking that the 400
// response body is the actual user-facing message, not a generic failure.
func TestAdminCreateProductRejectsHEICWithClearMessage(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category 7", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	heic := []byte{0, 0, 0, 28, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0, 0, 0, 0, 'm', 'i', 'f', '1', 'h', 'e', 'i', 'c'}
	body, contentType := buildMultipartProductForm(t, map[string]string{
		"name": "Should Not Exist Either", "price": "5.00", "category": "Test Photo Category 7",
	}, heic)

	r := httptest.NewRequest(http.MethodPost, "/admin/products", body)
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	AdminCreateProduct(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a HEIC upload", w.Code)
	}
	if !strings.Contains(w.Body.String(), "HEIC") {
		t.Errorf("response body = %q, want it to explain the photo is HEIC/HEIF and unsupported", w.Body.String())
	}

	products, _ := db.GetAllProducts()
	for _, p := range products {
		if p.Name == "Should Not Exist Either" {
			t.Fatal("product was created despite a rejected HEIC upload")
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

func TestAdminCreateProductRejectsInvalidPhotoWithoutCreatingProduct(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category 2", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	body, contentType := buildMultipartProductForm(t, map[string]string{
		"name":     "Should Not Exist",
		"price":    "5.00",
		"category": "Test Photo Category 2",
	}, []byte("this is not a real image file"))

	r := httptest.NewRequest(http.MethodPost, "/admin/products", body)
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	AdminCreateProduct(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a bogus photo upload", w.Code)
	}

	products, _ := db.GetAllProducts()
	for _, p := range products {
		if p.Name == "Should Not Exist" {
			t.Fatal("product was created despite an invalid photo upload — validation should happen before the DB write")
		}
	}
}

func TestAdminUpdateProductReplacesPhotoAndDeletesOldFile(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category 3", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	id, err := db.CreateProduct("Test Replace Item", 8.0, -1, "Test Photo Category 3", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	// First upload.
	body1, ct1 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Replace Item", "price": "8.00", "category": "Test Photo Category 3",
	}, testJPEG(t, 400, 300))
	r1 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body1)
	r1.Header.Set("Content-Type", ct1)
	r1 = withChiURLParam(r1, "id", strconv.Itoa(id))
	w1 := httptest.NewRecorder()
	AdminUpdateProduct(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first update status = %d, body = %s", w1.Code, w1.Body.String())
	}

	firstProduct, _ := db.GetProductByID(id)
	firstPath := firstProduct.ImageURL[1:]
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("first uploaded photo not found: %v", err)
	}
	defer os.Remove(firstPath)

	// Second upload — should replace the first and delete its file.
	body2, ct2 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Replace Item", "price": "8.00", "category": "Test Photo Category 3",
	}, testJPEG(t, 500, 400))
	r2 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body2)
	r2.Header.Set("Content-Type", ct2)
	r2 = withChiURLParam(r2, "id", strconv.Itoa(id))
	w2 := httptest.NewRecorder()
	AdminUpdateProduct(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second update status = %d, body = %s", w2.Code, w2.Body.String())
	}

	secondProduct, _ := db.GetProductByID(id)
	secondPath := secondProduct.ImageURL[1:]
	defer os.Remove(secondPath)

	if secondPath == firstPath {
		t.Fatal("second upload reused the exact same filename as the first — different photos should hash to distinct names")
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Errorf("old photo file %s should have been deleted after replacement, stat err = %v", firstPath, err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Errorf("new photo file not found: %v", err)
	}
}

func TestAdminUpdateProductRemovePhotoClearsURLAndDeletesFile(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category 4", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	id, err := db.CreateProduct("Test Remove Photo Item", 6.0, -1, "Test Photo Category 4", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	body1, ct1 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Remove Photo Item", "price": "6.00", "category": "Test Photo Category 4",
	}, testJPEG(t, 300, 300))
	r1 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body1)
	r1.Header.Set("Content-Type", ct1)
	r1 = withChiURLParam(r1, "id", strconv.Itoa(id))
	AdminUpdateProduct(httptest.NewRecorder(), r1)

	product, _ := db.GetProductByID(id)
	uploadedPath := product.ImageURL[1:]
	defer os.Remove(uploadedPath)

	body2, ct2 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Remove Photo Item", "price": "6.00", "category": "Test Photo Category 4", "remove_photo": "1",
	}, nil)
	r2 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body2)
	r2.Header.Set("Content-Type", ct2)
	r2 = withChiURLParam(r2, "id", strconv.Itoa(id))
	w2 := httptest.NewRecorder()
	AdminUpdateProduct(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w2.Code, w2.Body.String())
	}

	final, _ := db.GetProductByID(id)
	if final.ImageURL != "" {
		t.Errorf("ImageURL = %q, want empty after remove_photo", final.ImageURL)
	}
	if _, err := os.Stat(uploadedPath); !os.IsNotExist(err) {
		t.Errorf("photo file should have been deleted, stat err = %v", err)
	}
}

// TestAdminUpdateProductRoundTripsExistingLocalPhotoURL guards against a
// real bug: _product_list.html's hidden "image_url" input always resubmits
// the product's *current* value on every Save, even a plain stock/price
// edit that never touches the photo. Once a product has an uploaded photo,
// that value is a local "/static/uploads/..." path, not an http(s) URL —
// parseImageURL used to only accept http(s) or blank, so this exact,
// everyday request (no new "photo" file attached, just the existing local
// path round-tripping) 400'd, silently blocking *any* further edit —
// including the stock stepper and the Unlimited toggle — on any product
// that had ever had a photo. The other tests in this file never catch this
// because none of them set "image_url" on a follow-up request the way the
// real form does.
func TestAdminUpdateProductRoundTripsExistingLocalPhotoURL(t *testing.T) {
	if _, err := db.CreateCategory("Test Photo Category 5", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	id, err := db.CreateProduct("Test Round Trip Item", 8.0, 30, "Test Photo Category 5", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	// Upload a photo first, exactly like TestAdminUpdateProductReplacesPhotoAndDeletesOldFile.
	body1, ct1 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Round Trip Item", "price": "8.00", "category": "Test Photo Category 5", "stock": "30",
	}, testJPEG(t, 400, 300))
	r1 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body1)
	r1.Header.Set("Content-Type", ct1)
	r1 = withChiURLParam(r1, "id", strconv.Itoa(id))
	AdminUpdateProduct(httptest.NewRecorder(), r1)

	withPhoto, _ := db.GetProductByID(id)
	if withPhoto.ImageURL == "" {
		t.Fatal("setup: product should have an uploaded photo before the real test")
	}
	defer os.Remove(withPhoto.ImageURL[1:])

	// Now a plain stock change — no "photo" file attached, but "image_url"
	// carries the just-uploaded local path, exactly as _product_list.html's
	// hidden field would on a real Save click.
	body2, ct2 := buildMultipartProductForm(t, map[string]string{
		"name": "Test Round Trip Item", "price": "8.00", "category": "Test Photo Category 5",
		"image_url": withPhoto.ImageURL, "stock": "77",
	}, nil)
	r2 := httptest.NewRequest(http.MethodPost, "/admin/products/"+strconv.Itoa(id), body2)
	r2.Header.Set("Content-Type", ct2)
	r2 = withChiURLParam(r2, "id", strconv.Itoa(id))
	w2 := httptest.NewRecorder()
	AdminUpdateProduct(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s — a plain stock edit should succeed even though image_url still points at the uploaded photo", w2.Code, w2.Body.String())
	}

	final, _ := db.GetProductByID(id)
	if final.Stock != 77 {
		t.Errorf("Stock = %d, want 77 — the stock edit should have applied", final.Stock)
	}
	if final.ImageURL != withPhoto.ImageURL {
		t.Errorf("ImageURL = %q, want unchanged %q", final.ImageURL, withPhoto.ImageURL)
	}
}

// withChiURLParam attaches a chi route param the way the real router would,
// so a handler's chi.URLParam(r, "id") call resolves correctly when the
// handler is invoked directly (bypassing the router) in a test.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// A "../" image_url must never be stored: deleteLocalPhotoIfAny would later
// os.Remove it (e.g. the live bitebox.db).
func TestParseImageURLRejectsPathTraversal(t *testing.T) {
	for _, u := range []string{
		"/static/uploads/products/../../../bitebox.db",
		"/static/demo/../../templates/login.html",
		"/static/uploads/products/1/./x_full.jpg",
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"image_url": {u}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, ok := parseImageURL(r); ok {
			t.Errorf("parseImageURL accepted %q", u)
		}
	}
}
