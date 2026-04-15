import os

# Force direct-dns mode so tests don't try to read ServiceAccount tokens.
os.environ.setdefault("RUNTIME_POOL_ENDPOINT_MODE", "direct-dns")
os.environ.setdefault("REDIS_URL", "redis://localhost:6379/0")
os.environ.setdefault("AGENTS_NAMESPACE", "agents")
