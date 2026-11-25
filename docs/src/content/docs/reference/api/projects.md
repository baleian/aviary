---
title: Projects and members
description: The requests for projects, their members, the roles and the caller.
---

[Projects and members](../../use/projects.md) describes what a role allows.

## Projects

### Create a project

```http
POST /v1/projects
```

Creates a project. Body: `{"slug": "...", "name": "..."}`. Returns 409 if another project has the slug. Administrators only.

### List projects

```http
GET /v1/projects
```

Lists the caller's projects, oldest first: `{"projects": [...]}`. These are the projects that the caller or one of the caller's groups is a member of, and all projects for an administrator.

### Read a project

```http
GET /v1/projects/{id}
```

Returns the project.

## Members

### List members

```http
GET /v1/projects/{id}/members
```

Lists the members of the project in the order in which they were added: `{"members": [...]}`. See [Roles](../../use/projects.md#roles).

### Add a member

```http
POST /v1/projects/{id}/members
```

Adds a user or a group to the project. Body: `{"user": "..."}` with a username, or `{"group": "..."}` with the name of a group of the identity provider, and with it `{"role": "..."}` or `{"permissions": [...]}`. Administrators only. Returns 409 if the project already has that member.

### Change a member

```http
PUT /v1/members/{id}
```

Gives a member another role, or other permissions of its own. Body: `{"role": "..."}` or `{"permissions": [...]}`. Administrators only.

### Remove a member

```http
DELETE /v1/members/{id}
```

Removes a member from its project. Returns 204. Administrators only.

## Roles

### List roles

```http
GET /v1/roles
```

Lists the roles and their permissions: `{"roles": [{"name": "editor", "permissions": [...]}, ...]}`. See [Roles](../../use/projects.md#roles).

### Change a role

```http
PUT /v1/roles/{name}
```

Sets the permissions of a role. Body: `{"permissions": [...]}`. Administrators only: other callers get 403. Returns 400 for an unknown permission and 404 for an unknown role.

## The caller

### Read the caller

```http
GET /v1/me
```

Returns the caller, as the identity provider identifies it. See [Authentication](../../operate/authentication.md).
