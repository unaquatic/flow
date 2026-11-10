package ui

// ShowUpdated adds a one-line notice that flow updated to release version,
// "Updated to v1.2.3", its version in the product's color, as
// ShowKeywordToast draws it. The first start of a release that an update
// installed shows it under the session info, as Service.Updated tells.
func (s *Service) ShowUpdated(version string) []UIEvent {
	return s.ShowKeywordToast("Updated to ", version, "")
}
