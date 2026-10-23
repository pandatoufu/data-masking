//go:build !windows && !linux

package uiprefs

import "github.com/pandatoufu/data-masking/internal/i18n"

// platformSystemLanguage 在未专门支持的平台回退 en（首启行为保守、可被用户覆盖）。
func platformSystemLanguage() i18n.Lang { return i18n.EN }
