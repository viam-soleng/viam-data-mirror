from typing import ClassVar, Mapping, Sequence, Any, Dict, Optional, Tuple, Final, List, cast
from typing_extensions import Self
from typing import Final

from viam.resource.types import RESOURCE_NAMESPACE_RDK, RESOURCE_TYPE_SERVICE

from viam.module.types import Reconfigurable
from viam.proto.app.robot import ComponentConfig
from viam.proto.common import ResourceName, Vector3
from viam.resource.base import ResourceBase
from viam.resource.types import Model, ModelFamily
from viam.app.viam_client import ViamClient
from viam.rpc.dial import DialOptions
from viam.services.generic import Generic
from viam.logging import getLogger
from viam.utils import ValueTypes, struct_to_dict
from viam.proto.app.data import Filter, TagsFilter
from viam.proto.app.data import BinaryID

import time
import asyncio
from pathlib import Path
import os
import mimetypes
import traceback

LOGGER = getLogger(__name__)

class mirror(Generic, Reconfigurable):
    
    MODEL: ClassVar[Model] = Model(ModelFamily("mcvella", "data"), "mirror")
    
    viam_api_key: str
    viam_api_key_id: str
    labels: list = []
    tags: list =  []
    dataset_id: str = ""
    mirror_path: str = str(Path.home()) + '/.viam/data_mirror'
    app_client: ViamClient
    sync_frequency: int = 60
    running = False
    delete = False

    # Constructor
    @classmethod
    def new(cls, config: ComponentConfig, dependencies: Mapping[ResourceName, ResourceBase]) -> Self:
        my_class = cls(config.name)
        my_class.reconfigure(config, dependencies)
        return my_class

    # Validates JSON Configuration
    @classmethod
    def validate(cls, config: ComponentConfig):
        api_key = config.attributes.fields["app_api_key"].string_value
        if api_key == "":
            raise Exception("app_api_key attribute is required")
        api_key_id = config.attributes.fields["app_api_key_id"].string_value
        if api_key_id == "":
            raise Exception("app_api_key_id attribute is required")
        return

    # Handles attribute reconfiguration
    def reconfigure(self, config: ComponentConfig, dependencies: Mapping[ResourceName, ResourceBase]):
        self.running = False
        self.dataset_id = config.attributes.fields["dataset_id"].string_value or ""
        self.tags = config.attributes.fields["tags"].list_value or []
        self.labels = config.attributes.fields["labels"].list_value or []
        self.api_key = config.attributes.fields["app_api_key"].string_value or ''
        self.api_key_id = config.attributes.fields["app_api_key_id"].string_value or ''
        self.delete = config.attributes.fields["delete"].bool_value or False
        self.sync_frequency = config.attributes.fields["sync_frequency"].number_value or self.sync_frequency
        mirror_path = config.attributes.fields["mirror_path"].string_value or ''
        if mirror_path != "":
            self.mirror_path =   os.path.join(str(Path.home()) + '/.viam/', mirror_path)
       
        asyncio.ensure_future(self.sync_loop())
        return

    async def sync_loop(self):
        self.app_client: ViamClient = await self.viam_connect()
        self.running = True

        while self.running:
            try:
                await self.do_sync()
                await asyncio.sleep(self.sync_frequency)
            except Exception as e:
                LOGGER.error(f'Error in sync loop: {e}')
                LOGGER.error(traceback.print_exc())
                await asyncio.sleep(1)

    async def do_sync(self):
        # first, get all files and paths current on machine so we 
        # can see if there are extra files that need to be removed
        current_files = {}
        for path, dirs, files in os.walk(self.mirror_path):
            for file in files:
                current_files[os.path.join(self.mirror_path, path, file)] = True
        
        filter_args = {}
        if self.dataset_id != "":
            filter_args['dataset_id'] = self.dataset_id
        if len(self.tags) > 0:
            filter_args['tags_filter'] =  TagsFilter(tags=self.tags)
        filter = Filter(**filter_args)
        if len(self.labels) > 0:
            filter_args['bbox_labels'] = self.labels
        filter = Filter(**filter_args)

        binary_args = {'filter': filter, 'include_binary_data': False}
        
        # we need to page through results
        done = False
        while not done:
            binary = await self.app_client.data_client.binary_data_by_filter(**binary_args)
            if len(binary[0]):
                for b in binary[0]:

                    if b.metadata.file_name != "":
                        file = b.metadata.file_name
                    else:
                        file = b.metadata.id + mimetypes.guess_extension(b.metadata.capture_metadata.mime_type)
                    file = os.path.join(self.mirror_path, file)
                    # check if file exists in target, if so assume its a match and do not re-write
                    if os.path.isfile(file):
                        LOGGER.debug(f"{file} exists already, skipping")
                    else:
                        # get actual binary data
                        id = BinaryID(
                            file_id=b.metadata.id,
                            organization_id=b.metadata.capture_metadata.organization_id,
                            location_id=b.metadata.capture_metadata.location_id
                        )
                        data = await self.app_client.data_client.binary_data_by_ids([id])
                        self.write_file(file, data[0].binary)
                    current_files[file] = False
                # this is where the next page of data will start
                binary_args['last'] = binary[2]
            else:
                done = True
        
        if self.delete:
            # loop through and delete any extra files in the target
            for file in current_files:
                if current_files[file]:
                    os.remove(file)
                    LOGGER.info(f"Deleted {file}")
    
    async def viam_connect(self) -> ViamClient:
        dial_options = DialOptions.with_api_key( 
            api_key=self.api_key,
            api_key_id=self.api_key_id
        )
        return await ViamClient.create_from_dial_options(dial_options)
    
    async def do_command(
            self,
            command: Mapping[str, ValueTypes],
            *,
            timeout: Optional[float] = None,
            **kwargs
        ) -> Mapping[str, ValueTypes]:
        result = {}


    def write_file(self, file_path, content):
        path = Path(file_path)
        
        try:
            # Create the directory and any missing parent directories
            path.parent.mkdir(parents=True, exist_ok=True)
            
            with path.open('wb') as file:
                file.write(content)
            
            LOGGER.info(f"File successfully written: {file_path}")
        except IOError as e:
            LOGGER.error(f"An error occurred while writing the file: {e}")
        except Exception as e:
            LOGGER.error(f"An unexpected error occurred: {e}")