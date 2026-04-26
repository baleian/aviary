"""Loader for the project-root config.yaml that drives direct-mode LLM connections."""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

import yaml


@dataclass(frozen=True)
class BackendModel:
    backend: str
    model: str
    api_base: str | None = None
    api_key: str | None = None
    max_tokens: int | None = None
    capabilities: list[str] = field(default_factory=list)

    @property
    def qualified_name(self) -> str:
        return f"{self.backend}/{self.model}"


class LLMBackendsConfig:
    def __init__(self, models: list[BackendModel]) -> None:
        self._models = models
        self._by_qualified: dict[str, BackendModel] = {m.qualified_name: m for m in models}
        self._by_pair: dict[tuple[str, str], BackendModel] = {
            (m.backend, m.model): m for m in models
        }

    @property
    def models(self) -> list[BackendModel]:
        return list(self._models)

    def resolve(self, backend: str, model: str) -> BackendModel | None:
        hit = self._by_qualified.get(model)
        if hit is not None:
            return hit
        hit = self._by_pair.get((backend, model))
        if hit is not None:
            return hit
        prefix = f"{backend}/"
        if model.startswith(prefix):
            return self._by_pair.get((backend, model[len(prefix):]))
        return None


def load_config(path: str | Path) -> LLMBackendsConfig:
    raw = yaml.safe_load(Path(path).read_text()) or {}
    backends = raw.get("llm_backends") or {}
    models: list[BackendModel] = []
    for backend_name, entries in backends.items():
        for entry in entries or []:
            models.append(
                BackendModel(
                    backend=backend_name,
                    model=entry["model"],
                    api_base=entry.get("api_base"),
                    api_key=entry.get("api_key"),
                    max_tokens=entry.get("max_tokens"),
                    capabilities=list(entry.get("capabilities") or []),
                )
            )
    return LLMBackendsConfig(models)
