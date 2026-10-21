package scanner

const maxMetadataSize = 4 << 20

const maxJarSize = 64 << 20

type ArtifactInventory struct {
	Files        []string            `json:"files,omitempty"`
	Dependencies []string            `json:"dependencies,omitempty"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
	Image        *DockerImageDetails `json:"image,omitempty"`
}

type DockerImageDetails struct {
	Config []string `json:"config"`
	Tags   []string `json:"tags,omitempty"`
	Layers []string `json:"layers"`
}

type Result struct {
	Name      string            `json:"name"`
	Path      string            `json:"path"`
	Type      string            `json:"type"`
	Inventory ArtifactInventory `json:"inventory,omitempty"`
	Errors    []string          `json:"errors,omitempty"`
	latestKey string
	latestTag string
}

type dockerManifestEntry struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

type archiveContents struct {
	files              []string
	manifestFound      bool
	manifest           []byte
	manifestReadErr    error
	chartFiles         map[string][]byte
	javaFiles          map[string][]byte
	javaApplications   []javaApplication
	javaIndicatorsSeen bool
	errs               []string
}
