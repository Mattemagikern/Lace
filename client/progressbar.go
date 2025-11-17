package client

import (
	"os"
	"time"

	"github.com/schollz/progressbar/v3"
)

func newProgressBar(bytes int, desc string) *progressbar.ProgressBar {
	return progressbar.NewOptions(
		bytes,
		progressbar.OptionSetDescription(desc),
		progressbar.OptionSetWriter(os.Stderr),
	
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowTotalBytes(true),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionThrottle(20*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetRenderBlankState(true),

		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
			}),
		)
}
