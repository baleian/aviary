"""Per-user Vault credentials — list/write/delete.

Schema is derived from two sources:
  * the platform namespace (``aviary``) — anthropic-api-key + github-token,
    always offered regardless of MCP catalog state;
  * each MCP server the caller can see whose tools advertise required vault
    keys via ``inputSchema["x-aviary-required-credentials"]`` in the gateway's
    ``tools/list`` response.

When Vault is unconfigured (``VAULT_ADDR`` / ``VAULT_TOKEN`` unset) the
endpoint reports ``vault_enabled: false`` and refuses writes — only
config.yaml's ``secrets:`` table is read.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, status

from aviary_shared.config_secrets import load_secrets
from aviary_shared.vault import PLATFORM_NAMESPACE, VaultClient
from app.auth.dependencies import get_session_data
from app.auth.session_store import SessionData
from app.config import settings
from app.schemas.credentials import (
    CredentialKeyStatus,
    CredentialNamespace,
    CredentialsResponse,
    CredentialWriteRequest,
)
from app.services import local_mcp_catalog, mcp_catalog

router = APIRouter()

PLATFORM_KEYS = ["anthropic-api-key", "github-token"]
TOOL_NAME_SEPARATOR = "__"
CREDENTIAL_KEYS_FIELD = "x-aviary-required-credentials"

_ACRONYMS = {"api", "url", "id"}


def _humanize(slug: str) -> str:
    parts = slug.replace("_", "-").split("-")
    return " ".join(p.upper() if p.lower() in _ACRONYMS else p.capitalize() for p in parts)


def _vault() -> VaultClient:
    return VaultClient(settings.vault_addr, settings.vault_token)


def _server_credential_keys(tools: list[dict]) -> dict[str, list[str]]:
    """Aggregate per-server vault keys from tool inputSchema metadata.
    Order preserves first-seen across the catalog for stable UI rendering."""
    out: dict[str, list[str]] = {}
    for tool in tools:
        name = tool.get("name") or ""
        if TOOL_NAME_SEPARATOR not in name:
            continue
        server = name.split(TOOL_NAME_SEPARATOR, 1)[0]
        keys = (tool.get("inputSchema") or {}).get(CREDENTIAL_KEYS_FIELD) or []
        if not keys:
            continue
        existing = out.setdefault(server, [])
        for k in keys:
            if k and k not in existing:
                existing.append(k)
    return out


def _platform_namespace_spec() -> tuple[str, str, str | None, list[str]]:
    return (
        PLATFORM_NAMESPACE,
        "Aviary platform",
        "Always required — used for inference and the runtime sandbox.",
        PLATFORM_KEYS,
    )


async def _gather_namespaces(
    session: SessionData,
) -> list[tuple[str, str, str | None, list[str]]]:
    """``[(namespace, label, description, [keys])]`` — platform first,
    then any MCP server the caller can see that advertises required keys."""
    out: list[tuple[str, str, str | None, list[str]]] = [_platform_namespace_spec()]

    gateway = await mcp_catalog.fetch_tools(
        session.id_token or "", session.user_external_id,
    )
    local = await local_mcp_catalog.fetch_all_tools()
    server_keys = _server_credential_keys(gateway + local)
    for server in sorted(server_keys):
        out.append((server, _humanize(server), None, server_keys[server]))
    return out


async def _is_configured(sub: str, namespace: str, key: str) -> bool:
    if settings.vault_enabled:
        value = await _vault().read_user_credential(sub, namespace, key)
    else:
        value = load_secrets(settings.llm_backends_config_path).lookup(
            sub, namespace, key,
        )
    return bool(value)


async def _build_namespace_response(
    sub: str, namespace: str, label: str, description: str | None, keys: list[str],
) -> CredentialNamespace:
    statuses: list[CredentialKeyStatus] = []
    for key in keys:
        statuses.append(
            CredentialKeyStatus(
                key=key,
                label=_humanize(key),
                configured=await _is_configured(sub, namespace, key),
            )
        )
    return CredentialNamespace(
        namespace=namespace, label=label, description=description, keys=statuses,
    )


def _validate_known(namespaces: list[CredentialNamespace], ns: str, key: str) -> None:
    for entry in namespaces:
        if entry.namespace != ns:
            continue
        if any(k.key == key for k in entry.keys):
            return
        break
    raise HTTPException(
        status_code=status.HTTP_404_NOT_FOUND,
        detail=f"Unknown credential '{ns}/{key}'",
    )


def _require_vault() -> None:
    if not settings.vault_enabled:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="Vault is not configured — credentials are read-only.",
        )


@router.get("", response_model=CredentialsResponse)
async def list_credentials(session: SessionData = Depends(get_session_data)):
    namespaces = await _gather_namespaces(session)
    out = [
        await _build_namespace_response(session.user_external_id, *spec)
        for spec in namespaces
    ]
    return CredentialsResponse(vault_enabled=settings.vault_enabled, namespaces=out)


@router.put("/{namespace}/{key}", status_code=204)
async def write_credential(
    namespace: str,
    key: str,
    body: CredentialWriteRequest,
    session: SessionData = Depends(get_session_data),
):
    _require_vault()
    namespaces = await _gather_namespaces(session)
    rendered = [
        await _build_namespace_response(session.user_external_id, *spec)
        for spec in namespaces
    ]
    _validate_known(rendered, namespace, key)
    await _vault().write_user_credential(
        session.user_external_id, namespace, key, body.value,
    )
    return None


@router.delete("/{namespace}/{key}", status_code=204)
async def delete_credential(
    namespace: str,
    key: str,
    session: SessionData = Depends(get_session_data),
):
    _require_vault()
    namespaces = await _gather_namespaces(session)
    rendered = [
        await _build_namespace_response(session.user_external_id, *spec)
        for spec in namespaces
    ]
    _validate_known(rendered, namespace, key)
    await _vault().delete_user_credential(session.user_external_id, namespace, key)
    return None
