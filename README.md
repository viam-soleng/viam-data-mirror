# Viam data mirror modular resource

This module implements the [rdk generic API](https://github.com/rdk/generic-api) in a mcvella:data:mirror model.
With this model, you can set up a periodic sync of binary data from Viam's data management to machines running viam-server.

For example, perhaps you have a set of images in Viam's data management that you'll be using as embeddings for computer vision on edge devices.
This module allows you to point to this set of images by tags and/or dataset, and a target directory on the machines running viam-server, and the images will be copied and kept in-sync with data management on the target machine.

Note that binary data can be uploaded to Viam's Data Management with or without a "file_name".
If a "file_name" exists, it will be used to write the file on the target machine.
If "file_name" contains a path (e.g. "/path/to/file.jpg") then that path will be created in the target.
If "file_name" does not exist, the file will be written with the ID of the binary data in data management plus an extension based on MIME type stored in data management.

Also note that this module does not differentiate between files with the same "file_name", so if a file with am matching "file_name" exists on the target, it will assume it is the same file and not overwrite the file on the target.

## Build and run

To use this module, follow the instructions to [add a module from the Viam Registry](https://docs.viam.com/registry/configure/#add-a-modular-resource-from-the-viam-registry) and select the `rdk:generic:mcvella:data:mirror` model from the [`mcvella:data:mirror` module](https://app.viam.com/module/rdk/mcvella:data:mirror).

## Configure your data mirror

> [!NOTE]  
> Before configuring your data mirror, you must [create a machine](https://docs.viam.com/manage/fleet/machines/#add-a-new-machine).

Navigate to the **Config** tab of your machine's page in [the Viam app](https://app.viam.com/).
Click on the **Components** subtab and click **Create component**.
Select the `generic` type, then select the `mcvella:data:mirror` model.
Click **Add module**, then enter a name for your generic and click **Create**.

On the new component panel, copy and paste the following attribute template into your generic’s **Attributes** box:

```json
{
  "delete": false,
  "tags": [
    "face",
    "head"
  ],
  "app_api_key": "abc123",
  "app_api_key_id": "abc-123",
  "sync_frequency": 10
}
```

> [!NOTE]  
> For more information, see [Configure a Machine](https://docs.viam.com/manage/configuration/).

### Attributes

The following attributes are available for `mcvella:data:mirror`:

| Name | Type | Inclusion | Description |
| ---- | ---- | --------- | ----------- |
| `app_api_key` | string | **Required** |  API key that has access to Viam data management |
| `app_api_key_id` | string | **Required** |  API key ID that has access to Viam data management |
| `sync_frequency` | integer | Optional |  How often to check sync changes, in seconds.  Default is 60. |
| `tags` | list | Optional |  Data management tags to filter on. |
| `labels` | list | Optional |  Data management labels to filter on. |
| `dataset_id` | string | Optional |  Data management dataset ID to filter on. |
| `mirror_path` | string | Optional | Path on target machine to sync to, defaults to <home>/.viam/data_mirror. Requested path will be created relative to <home>/.viam/ |
| `delete` | boolean | Optional |  If set to true, will delete files in mirror_path that do not exist in data management. |
