package controller

import (
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"net/http"
	"net/url"
	"sort"
)

func resourceTagQuery(writer http.ResponseWriter, request *http.Request) ([]string, bool) {
	if len(request.URL.RawQuery) > 2048 {
		localapi.Error(writer, http.StatusBadRequest, "INVALID_QUERY", "Invalid resource tag query.")
		return nil, false
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err == nil {
		for name := range query {
			if name != "tag" {
				err = protocol.ErrInvalidTags
			}
		}
	}
	if err != nil || protocol.ValidateTags(query["tag"]) != nil {
		localapi.Error(writer, http.StatusBadRequest, "INVALID_QUERY", "Invalid resource tag query.")
		return nil, false
	}
	return query["tag"], true
}

// Only already-authorized inventory may be passed here; tags grant no access.
func filterResourceTags(resources []protocol.ResourceDescriptor, tags []string) []protocol.ResourceDescriptor {
	filtered := make([]protocol.ResourceDescriptor, 0, len(resources))
	for _, resource := range resources {
		if protocol.MatchesTags(resource.Tags, tags) {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

func resourceTags(resources []protocol.ResourceDescriptor) []string {
	seen := make(map[string]bool)
	for _, resource := range resources {
		for _, tag := range resource.Tags {
			seen[tag] = true
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}
