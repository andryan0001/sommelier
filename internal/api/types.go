package api

// Wire types for nhentai API v2. Field names mirror the JSON exactly; anything
// nullable upstream is a pointer here so "absent" and "empty" stay distinct.

// GalleryListItem is what /search, /galleries and /galleries/tagged return.
// It is deliberately thin: no tag names, no scanlator, no upload date.
type GalleryListItem struct {
	ID              int     `json:"id"`
	MediaID         string  `json:"media_id"`
	EnglishTitle    string  `json:"english_title"`
	JapaneseTitle   *string `json:"japanese_title"`
	Thumbnail       string  `json:"thumbnail"`
	ThumbnailWidth  int     `json:"thumbnail_width"`
	ThumbnailHeight int     `json:"thumbnail_height"`
	NumPages        int     `json:"num_pages"`
	NumFavorites    int     `json:"num_favorites"`
	TagIDs          []int   `json:"tag_ids"`
	Blacklisted     bool    `json:"blacklisted"`
}

// GalleryList is the paginated envelope used by every list endpoint.
type GalleryList struct {
	Result   []GalleryListItem `json:"result"`
	Total    int               `json:"total"`
	PerPage  int               `json:"per_page"`
	NumPages int               `json:"num_pages"`
}

// Tag is a taxonomy entry. Description is written by hand upstream and is the
// only prose the API exposes, which makes it the planner's semantic layer.
type Tag struct {
	ID          int     `json:"id"`
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Count       int     `json:"count"`
	Description *string `json:"description"`
}

// Title is the three-title block on a gallery detail.
type Title struct {
	English  string  `json:"english"`
	Japanese *string `json:"japanese"`
	Pretty   string  `json:"pretty"`
}

// Cover describes one image on the CDN. The path is used verbatim, never
// reconstructed: nhentai bans clients that guess at URL patterns.
type Cover struct {
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Gallery is the full detail record from /galleries/{id}.
type Gallery struct {
	ID           int     `json:"id"`
	MediaID      string  `json:"media_id"`
	Title        Title   `json:"title"`
	Cover        Cover   `json:"cover"`
	Thumbnail    Cover   `json:"thumbnail"`
	Scanlator    string  `json:"scanlator"`
	UploadDate   int64   `json:"upload_date"`
	Tags         []Tag   `json:"tags"`
	NumPages     int     `json:"num_pages"`
	NumFavorites int     `json:"num_favorites"`
}
