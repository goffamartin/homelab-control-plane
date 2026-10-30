package localapi

// Old controllers ignore unknown resource-list query parameters. Clients must
// require this acknowledgement before presenting a tag-filtered result.
const ResourceTagsHeader = "X-HLCP-Resource-Tags"
