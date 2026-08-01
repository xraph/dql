package expand

import "context"

// The expander turns opaque id columns — created_by, owner_team — into the
// display fields a UI needs. What an "id" resolves to is the embedder's
// business, so this package declares only the narrow shape it consumes and
// leaves the embedder to satisfy it.
//
// These interfaces are deliberately smaller than any real directory: the
// expander calls exactly two methods and reads six fields. Declaring the whole
// upstream interface here would import a directory model this package has no
// use for.

// User is the subset of a directory user the expander renders.
type User struct {
	ID          string
	DisplayName string
	Email       string
	AvatarURL   string
}

// Team is the subset of a directory team the expander renders.
type Team struct {
	ID   string
	Name string
}

// UserResolver looks up a single user by id. Returning (nil, nil) for an
// unknown id is fine — the expander leaves the column unexpanded.
type UserResolver interface {
	GetUser(ctx context.Context, userID string) (*User, error)
}

// TeamResolver looks up a single team by id, with the same nil contract.
type TeamResolver interface {
	GetTeam(ctx context.Context, teamID string) (*Team, error)
}
