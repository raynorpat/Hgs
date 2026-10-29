package setting

import "fmt"

const OfficialGravatarSource = "https://gravatar.com/avatar/"
const LegacyGravatarSource = "https://gravatar.duoshuo.com/avatar/"

// ResolveGravatarSource accepts only supported operator-configured providers.
// Unknown values fail closed; in particular no arbitrary URL or HTTP downgrade
// becomes an outbound image request.
func ResolveGravatarSource(value string) (string, error) {
	switch value {
	case "gravatar", OfficialGravatarSource:
		return OfficialGravatarSource, nil
	case "duoshuo", LegacyGravatarSource:
		return LegacyGravatarSource, nil
	default:
		return "", fmt.Errorf("unsupported GRAVATAR_SOURCE; use gravatar or duoshuo over HTTPS")
	}
}
