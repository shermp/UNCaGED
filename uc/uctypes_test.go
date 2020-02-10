package uc

import (
	"encoding/json"
	"testing"
)

func TestMetaUnmarshal(t *testing.T) {
	meta := CalibreBookMeta{}
	testMetaStr := []byte(`
	  {
		"rating": null, 
		"author_sort": "Author, Test", 
		"size": 586110, 
		"series_index": null, 
		"cover": null, 
		"publication_type": null, 
		"db_id": null, 
		"series": null, 
		"rights": null, 
		"title": "Test Title", 
		"identifiers": {}, 
		"last_modified": "None", 
		"thumbnail": null, 
		"timestamp": "2020-02-10T22:12:41+13:00", 
		"uuid": null, 
		"mime": "application/epub+zip", 
		"pubdate": null, 
		"publisher": null, 
		"application_id": null, 
		"user_metadata": {}, 
		"author_sort_map": {
			"Test Author": "",
			"Joe Bloggs": ""
		}, 
		"authors": [
		  "Test Author",
		  "Joe Bloggs"
		], 
		"comments": null, 
		"languages": [], 
		"book_producer": null, 
		"lpath": "books/Test Author - Test Title.epub", 
		"user_categories": {}, 
		"author_link_map": {}, 
		"title_sort": null, 
		"tags": []
	  }`)
	if err := json.Unmarshal(testMetaStr, &meta); err != nil {
		t.Errorf("Error umarshalling JSON: %v", err)
	}
	if meta.LastModified.GetTime() != nil || meta.Pubdate.GetTime() != nil || meta.Timestamp.GetTime() == nil {
		t.Errorf("Expected: <nil>, <nil>, time.Time\nGot: %v, %v, %v", meta.LastModified, meta.Pubdate, meta.Timestamp)
	}
}
