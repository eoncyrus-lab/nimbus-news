package source

import (
	"testing"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

func TestParseTelegramPostLink(t *testing.T) {
	tests := []struct {
		link    string
		channel string
		postID  string
		ok      bool
	}{
		{"https://t.me/thekobeissiletter/8506", "thekobeissiletter", "8506", true},
		{"https://t.me/BWEtradfi/7700?single", "bwetradfi", "7700", true},
		{"https://telegram.me/WalterBloomberg/12345", "walterbloomberg", "12345", true},
		{"https://t.me/s/TheKobeissiLetter", "", "", false},
		{"https://t.me/joinchat/abc", "", "", false},
		{"https://example.com/channel/123", "", "", false},
		{"https://t.me/thekobeissiletter/notanumber", "", "", false},
	}
	for _, test := range tests {
		channel, postID, ok := parseTelegramPostLink(test.link)
		if ok != test.ok || channel != test.channel || postID != test.postID {
			t.Fatalf("%s => (%q, %q, %v), want (%q, %q, %v)", test.link, channel, postID, ok, test.channel, test.postID, test.ok)
		}
	}
}

func TestParseTelegramPreviewImages(t *testing.T) {
	page := `
<div class="tgme_widget_message" data-post="TheKobeissiLetter/8506">
  <a class="tgme_widget_message_photo_wrap blured" href="https://t.me/thekobeissiletter/8506"
     style="width:537px;background-image:url('https://cdn4.telesco.pe/file/abc')"></a>
</div>
<div class="tgme_widget_message" data-post="TheKobeissiLetter/8507">
  <div class="tgme_widget_message_text">text only, og:image would be the channel avatar</div>
</div>
<div class="tgme_widget_message" data-post="TheKobeissiLetter/8508">
  <div class="tgme_widget_message_video_player">
    <i class="tgme_widget_message_video_thumb" style="background-image:url(https://cdn4.telesco.pe/file/vid)"></i>
  </div>
</div>`
	images := parseTelegramPreviewImages(page)
	if got := images["thekobeissiletter/8506"]; got != "https://cdn4.telesco.pe/file/abc" {
		t.Fatalf("photo post image=%q", got)
	}
	if got := images["thekobeissiletter/8508"]; got != "https://cdn4.telesco.pe/file/vid" {
		t.Fatalf("video post thumb=%q", got)
	}
	if _, ok := images["thekobeissiletter/8507"]; ok {
		t.Fatal("text-only post must not produce an image")
	}
}

func TestExtractOGImage(t *testing.T) {
	propertyFirst := `<html><head><meta property="og:image" content="https://cdn.example.com/a.jpg"></head></html>`
	if got := extractOGImage(propertyFirst); got != "https://cdn.example.com/a.jpg" {
		t.Fatalf("property-first og:image=%q", got)
	}
	contentFirst := `<html><head><meta content="https://cdn.example.com/b.jpg" name="twitter:image"></head></html>`
	if got := extractOGImage(contentFirst); got != "https://cdn.example.com/b.jpg" {
		t.Fatalf("content-first twitter:image=%q", got)
	}
	if got := extractOGImage(`<html><head><title>x</title></head></html>`); got != "" {
		t.Fatalf("no meta image got=%q", got)
	}
}

func TestValidImageURLRejectsNonHTTPSAndPrivateHosts(t *testing.T) {
	valid := []string{
		"https://cdn4.telesco.pe/file/abc",
		"https://example.com/photo.jpg",
	}
	for _, raw := range valid {
		if !validImageURL(raw) {
			t.Fatalf("%s should be valid", raw)
		}
	}
	invalid := []string{
		"http://example.com/photo.jpg",
		"https://127.0.0.1/photo.jpg",
		"https://localhost/photo.jpg",
		"https://[::1]/photo.jpg",
		"https://10.0.0.1/photo.jpg",
		"https://192.168.1.2/photo.jpg",
		"https://printer.local/photo.jpg",
		"not a url",
		"",
	}
	for _, raw := range invalid {
		if validImageURL(raw) {
			t.Fatalf("%s should be rejected", raw)
		}
	}
}

func TestResolveImagesKeepsOnlyFirstFeedImage(t *testing.T) {
	src := &RSSSource{name: "test", imageCache: make(map[string]imageCacheEntry)}
	msg := &model.Message{
		ImageURL:  "https://cdn.example.com/first.jpg",
		ImageURLs: []string{"https://cdn.example.com/first.jpg", "https://cdn.example.com/second.jpg"},
	}
	src.resolveImages([]*model.Message{msg})
	if msg.ImageURL != "https://cdn.example.com/first.jpg" || len(msg.ImageURLs) != 1 {
		t.Fatalf("image fields after resolve: url=%q urls=%v", msg.ImageURL, msg.ImageURLs)
	}
}
