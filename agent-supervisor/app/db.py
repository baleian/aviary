from aviary_shared.db import create_session_factory
from app.config import settings

engine, async_session = create_session_factory(settings.database_url)
