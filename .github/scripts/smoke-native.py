#!/usr/bin/env python3
"""Load the built native ABI without credentials, OAuth, HTTP, or host callbacks."""
import base64
import ctypes
import json
from argparse import ArgumentParser
from hashlib import sha256
from pathlib import Path

from release_metadata import PLUGIN_ID, read_metadata


class Buffer(ctypes.Structure):
    _fields_ = [("ptr", ctypes.c_void_p), ("len", ctypes.c_size_t)]


class HostAPI(ctypes.Structure):
    _fields_ = [("abi_version", ctypes.c_uint32), ("host_ctx", ctypes.c_void_p), ("call", ctypes.c_void_p), ("free_buffer", ctypes.c_void_p)]


class PluginAPI(ctypes.Structure):
    _fields_ = [("abi_version", ctypes.c_uint32), ("call", ctypes.c_void_p), ("free_buffer", ctypes.c_void_p), ("shutdown", ctypes.c_void_p)]


def smoke(library, version, descriptor):
    native = ctypes.CDLL(str(library.resolve()))
    native.cliproxy_plugin_init.argtypes = [ctypes.POINTER(HostAPI), ctypes.POINTER(PluginAPI)]
    native.cliproxy_plugin_init.restype = ctypes.c_int
    host = HostAPI(abi_version=1)
    plugin = PluginAPI()
    if native.cliproxy_plugin_init(ctypes.byref(host), ctypes.byref(plugin)) != 0 or plugin.abi_version != 1:
        raise ValueError("native plugin initialization failed")
    call = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_char_p, ctypes.c_void_p, ctypes.c_size_t, ctypes.POINTER(Buffer))(plugin.call)
    free = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_size_t)(plugin.free_buffer)
    shutdown = ctypes.CFUNCTYPE(None)(plugin.shutdown)

    def rpc(method, request):
        raw = json.dumps(request).encode("utf-8")
        source = ctypes.create_string_buffer(raw)
        response = Buffer()
        rc = call(method.encode("ascii"), source, len(raw), ctypes.byref(response))
        try:
            envelope = json.loads(ctypes.string_at(response.ptr, response.len))
        finally:
            free(response.ptr, response.len)
        if rc != 0 or envelope.get("ok") is not True:
            raise ValueError(f"native {method} failed")
        return envelope["result"]

    try:
        registration = rpc("plugin.register", {})
        if registration["metadata"]["Version"] != version:
            raise ValueError("native registration differs from release version")
        resources = rpc("management.register", {})["resources"]
        if [entry["path"] for entry in resources] != ["/oauth-select", "/oauth-start", "/zai.png", "/bigmodel.png", "/omc.json"] or any(entry["menu"] for entry in resources):
            raise ValueError("native resource registration or empty-menu contract changed")
        path = f"/v0/resource/plugins/{PLUGIN_ID}/omc.json"
        response = rpc("management.handle", {"Method": "GET", "Path": path})
        if response["StatusCode"] != 200 or response["Headers"].get("Content-Type") != ["application/json; charset=utf-8"]:
            raise ValueError("native descriptor HTTP response contract failed")
        if base64.b64decode(response["Body"], validate=True) != descriptor:
            raise ValueError("native resource bytes differ from release attachment source")
        response = rpc("management.handle", {"Method": "GET", "Path": path + "/"})
        if response["StatusCode"] != 404:
            raise ValueError("descriptor handler accepted a nonexact resource path")
        response = rpc("management.handle", {"Method": "POST", "Path": path})
        if response["StatusCode"] != 405:
            raise ValueError("descriptor handler accepted a non-GET request")
        print(f"native ABI/resource verified: {library.name}, version={version}, descriptor sha256={sha256(descriptor).hexdigest()}")
    finally:
        shutdown()


def main():
    parser = ArgumentParser(description=__doc__)
    parser.add_argument("--library", required=True, type=Path)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    version, descriptor = read_metadata(version=args.version)
    smoke(args.library, version, descriptor)


if __name__ == "__main__":
    main()
