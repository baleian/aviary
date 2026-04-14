import uuid
from datetime import datetime
from typing import Any

from pydantic import BaseModel, ConfigDict, Field


class UserResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: uuid.UUID
    external_id: str
    email: str
    display_name: str
    avatar_url: str | None = None
    preferences: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime


class PreferencesUpdateRequest(BaseModel):
    """Partial update — top-level keys in `preferences` are merged with the
    stored dict (shallow merge, caller supplies whichever keys it owns)."""

    preferences: dict[str, Any]


class AuthConfigResponse(BaseModel):
    issuer: str
    client_id: str
    authorization_endpoint: str
    token_endpoint: str
    end_session_endpoint: str


class TokenExchangeRequest(BaseModel):
    code: str
    redirect_uri: str
    code_verifier: str
