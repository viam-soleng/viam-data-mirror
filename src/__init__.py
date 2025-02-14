"""
This file registers the model with the Python SDK.
"""

from viam.services.generic import Generic
from viam.resource.registry import Registry, ResourceCreatorRegistration

from .mirror import mirror

Registry.register_resource_creator(Generic.API, mirror.MODEL, ResourceCreatorRegistration(mirror.new, mirror.validate))
