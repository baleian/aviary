from __future__ import annotations

from functools import lru_cache

from aviary_shared.llm_backends import BackendModel, LLMBackendsConfig, load_config

from app.config import settings


@lru_cache(maxsize=1)
def _config() -> LLMBackendsConfig:
    return load_config(settings.llm_backends_config_path)


def resolve(backend: str, model: str) -> BackendModel | None:
    return _config().resolve(backend, model)
