package swifttest

// LoadCanonicalFixtures populates the §58 canonical fixture namespace:
// the classification traps every Aeronautes UI and job must get right.
// The same names appear in the frontend's static fixtures and, later, in
// the SAIO fixture loader (§57.3).
func (s *Server) LoadCanonicalFixtures() {
	put := func(container, name string, o Object) { s.PutObject(container, name, o) }

	// The primary namespace.
	put("fixtures", "normal/hello.txt", Object{Bytes: 12, ContentType: "text/plain", ETag: "5eb63bbbe01eeed093cb22bb8f5acdc3", Meta: map[string]string{"Origin": "fixture"}})
	put("fixtures", "empty-object", Object{Bytes: 0, ContentType: "application/octet-stream"})
	put("fixtures", "folder-marker/", Object{Bytes: 0, ContentType: "application/directory"})
	put("fixtures", "implied/child.txt", Object{Bytes: 2048, ContentType: "text/plain"})
	put("fixtures", "unicode/日本語.txt", Object{Bytes: 512, ContentType: "text/plain"})
	put("fixtures", "weird/a+b.txt", Object{Bytes: 64, ContentType: "text/plain"})
	put("fixtures", "weird/percent%name", Object{Bytes: 96})
	put("fixtures", "weird/repeated//slash", Object{Bytes: 128})
	put("fixtures", "external-dlo", Object{
		Bytes: 0, ContentType: "video/mp4",
		Manifest: "external-dlo-segments/wedding-video/",
		Meta:     map[string]string{"Shoot": "wedding", "Location": "sydney", "Camera": "sony-fx6"},
	})
	put("fixtures", "external-slo", Object{
		Bytes: 1024, ContentType: "application/x-tar",
		SLO: true, SLOEtag: "9f2b8c6e4d2a1f2b7e9d3c8a4b1f0e21",
	})

	// Segment stores: confirmed by manifest reference…
	put("external-dlo-segments", "wedding-video/0000000000000001", Object{Bytes: 1_000_000_000})
	put("external-dlo-segments", "wedding-video/0000000000000002", Object{Bytes: 1_000_000_000})
	put("external-dlo-segments", "wedding-video/0000000000000003", Object{Bytes: 1_000_000_000})
	put("slo-parts", "backup/part-000001", Object{Bytes: 1_000_000_000})
	put("slo-parts", "backup/part-000002", Object{Bytes: 1_000_000_000})
	put("slo-parts", "backup/part-000003", Object{Bytes: 1_100_000_000})
	s.AddContainer("confirmed_segments", "standard")

	// …and the heuristic traps: named like segment stores, actually normal.
	s.AddContainer("photos_segments", "ec42")
	put("photos_segments", "sunrise.jpg", Object{Bytes: 300_000_000, ContentType: "image/jpeg"})
	put("photos_segments", "sunset.jpg", Object{Bytes: 300_000_000, ContentType: "image/jpeg"})
	put("photos_segments", "harbour.jpg", Object{Bytes: 300_000_000, ContentType: "image/jpeg"})
	put(".file-segments", "blob/000001", Object{Bytes: 6_000_000})
	put(".file-segments", "blob/000002", Object{Bytes: 6_000_000})
}
