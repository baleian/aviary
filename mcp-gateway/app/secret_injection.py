"""Vault-injection config: which tool args are auto-filled from Vault.

Format (YAML):
  servers:
    <server_name>:
      args:                      # applies to all tools on the server
        <arg_name>:
          vault_key: <key>
      tools:
        <tool_name>:
          args:                  # per-tool overrides
            <arg_name>:
              vault_key: <key>

Vault path: secret/aviary/credentials/{user_external_id}/{vault_key}
"""

import copy
import logging

import yaml

from app.config import settings

logger = logging.getLogger(__name__)

INJECTED_VAULT_KEY_PROP = "x-injected-from-vault"

_INJECTION_CONFIG: dict[str, dict] = {}
try:
    with open(settings.secret_injection_config) as f:
        _raw = yaml.safe_load(f)
    _INJECTION_CONFIG = _raw.get("servers", {}) if _raw else {}
    logger.info("Loaded secret-injection config for %d servers", len(_INJECTION_CONFIG))
except FileNotFoundError:
    logger.warning("secret-injection.yaml not found at %s", settings.secret_injection_config)


def get_injected_args(server_name: str, tool_name: str) -> dict[str, dict]:
    server_cfg = _INJECTION_CONFIG.get(server_name, {})
    if not server_cfg:
        return {}
    result = dict(server_cfg.get("args", {}))
    tool_cfg = server_cfg.get("tools", {}).get(tool_name, {})
    if tool_cfg:
        result.update(tool_cfg.get("args", {}))
    return result


def strip_injected_from_schema(schema: dict, injected_args: dict[str, dict]) -> dict:
    if not injected_args:
        return schema
    schema = copy.deepcopy(schema)
    props = schema.get("properties", {})
    required = schema.get("required", [])
    for arg_name in injected_args:
        props.pop(arg_name, None)
        if arg_name in required:
            required = [r for r in required if r != arg_name]
    if props:
        schema["properties"] = props
    if required:
        schema["required"] = required
    elif "required" in schema:
        del schema["required"]
    return schema


def annotate_schema_with_injections(schema: dict, injected_args: dict[str, dict]) -> dict:
    if not injected_args:
        return schema
    props = schema.get("properties")
    if not isinstance(props, dict):
        return schema
    for arg_name, mapping in injected_args.items():
        prop = props.get(arg_name)
        if isinstance(prop, dict):
            prop[INJECTED_VAULT_KEY_PROP] = mapping.get("vault_key")
    return schema
