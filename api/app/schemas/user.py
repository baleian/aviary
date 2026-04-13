import uuid
from datetime import datetime

from pydantic import BaseModel, ConfigDict


class UserResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: uuid.UUID
    external_id: str
    email: str
    display_name: str
    created_at: datetime


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
