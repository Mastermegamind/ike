package sites

// GitSource is the repository a website deploys from.
type GitSource struct {
	Provider   string `json:"provider"` // github | gitlab | bitbucket | other
	URL        string `json:"url"`
	RefType    string `json:"ref_type"` // branch | tag | commit
	Ref        string `json:"ref"`      // main, v1.*, or a commit hash
	Subdir     string `json:"subdir,omitempty"`
	Auth       string `json:"auth"`               // none | deploy_key | token
	Username   string `json:"username,omitempty"` // token auth (Bitbucket app passwords, GitLab "oauth2")
	AutoDeploy bool   `json:"auto_deploy"`        // deploy on push
}
