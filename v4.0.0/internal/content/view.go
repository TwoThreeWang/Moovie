package content

import (
	"html/template"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

const (
	DefaultDescription = platformweb.DefaultDescription
	DefaultKeywords    = platformweb.DefaultKeywords
)

type Metadata = platformweb.Metadata
type ViewModel = platformweb.ViewModel

func newViewModel(c *gin.Context, cfg config.Config, metadata Metadata) ViewModel {
	return platformweb.NewViewModel(c, cfg, metadata)
}

func canonicalURL(siteURL, path string) string {
	return platformweb.CanonicalURL(siteURL, path)
}

func jsonLD(value any) (template.JS, error) {
	return platformweb.JSONLD(value)
}
