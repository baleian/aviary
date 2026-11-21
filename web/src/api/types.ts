// The resources of the API, as README.md describes them.

// The caller, from GET /v1/me. Without an identity provider the caller is
// nobody in particular, and only "admin" is set.
export interface Me {
  id?: string;
  subject?: string;
  username?: string;
  display_name?: string;
  groups?: string[];
  admin: boolean;
}

export interface Project {
  id: string;
  slug: string;
  // For display only, and empty if the project was given none.
  name: string;
  created_at: string;
}
