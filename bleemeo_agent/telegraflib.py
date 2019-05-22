#
#  Copyright 2015-2016 Bleemeo
#
#  bleemeo.com an infrastructure monitoring solution in the Cloud
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# ctypes.Structure don't have public methods.
# pylint: disable=too-few-public-methods

import ctypes
from ctypes import (c_char, c_char_p, c_float, c_int, c_void_p, cast, POINTER)
import os
import threading
import time

import bleemeo_agent.type


_lib = None  # pylint: disable=invalid-name
_lock = threading.Lock()  # pylint: disable=invalid-name


class _Tag(ctypes.Structure):
    _fields_ = [('tag_name', c_char_p),
                ('tag_value', c_char_p)]


class _MetricPoint(ctypes.Structure):
    _fields_ = [('input_id', c_int),
                ('name', c_char_p),
                ('tag', POINTER(_Tag)),
                ('tag_count', c_int),
                ('metric_type', c_int),
                ('value', c_float),
                ('time', c_int)]


class _MetricPointVector(ctypes.Structure):
    _fields_ = [('metric_point', POINTER(_MetricPoint)),
                ('metric_point_count', c_int)]


def _init_lib():
    global _lib  # pylint: disable=invalid-name,global-statement
    with _lock:
        if _lib is not None:
            return
        if os.path.exists("agentgo/libcabi.so"):
            lib_path = "agentgo/libcabi.so"
        else:
            lib_path = "libcabi.so"
        _lib = ctypes.cdll.LoadLibrary(lib_path)

        # Use POINTER(c_char) and not c_char_p to be able to deallocate the str
        _lib.LastError.restype = POINTER(c_char)
        _lib.InitGroup  # force load symbol pylint: disable=pointless-statement
        _lib.FreeGroup.restype = c_int
        _lib.FreeGroup.argtypes = [c_int]
        _lib.AddSimpleInput.argtypes = [c_int, c_char_p]
        _lib.AddNetworkInput.argtypes = [c_int, POINTER(c_char_p), c_int]
        _lib.AddDiskInput.argtypes = [
            c_int, c_char_p, POINTER(c_char_p), c_int]
        _lib.AddDiskIOInput.argtypes = [c_int, POINTER(c_char_p), c_int]
        _lib.AddInputWithAddress.argtypes = [c_int, c_char_p, c_char_p]
        _lib.Gather.restype = _MetricPointVector
        _lib.Gather.argtypes = [c_int]
        _lib.FreeMetricPointVector.restype = None
        _lib.FreeMetricPointVector.argtypes = [_MetricPointVector]
        _lib.free.restype = None
        _lib.free.argtypes = [c_void_p]


def _last_error():
    err_p = _lib.LastError()
    if not err_p:
        return None
    err = cast(err_p, c_char_p).value.decode('utf-8')
    _lib.free(err_p)
    return err


class Telegraflib:
    # pylint: disable=too-many-instance-attributes

    def __init__(  # pylint: disable=too-many-arguments
            self, is_terminated, emit_metric, network_blacklist,
            disk_mount_point, disk_blacklist, diskio_whitelist):
        self.emit_metric = emit_metric
        self.is_terminated = is_terminated
        self.network_blacklist = network_blacklist
        if disk_mount_point is None:
            disk_mount_point = "/"
        self.disk_mount_point = disk_mount_point
        self.disk_blacklist = disk_blacklist
        self.diskio_whitelist = diskio_whitelist
        _init_lib()
        self.inputs_id_map = {}
        self.input_group_id = _lib.InitGroup()
        if self.input_group_id < 0:
            last_err = _last_error()
            raise ValueError(
                "Failed to initialize Telegraf service input group: {}".format(
                    last_err,
                )
            )
        self.system_input_group_id = _lib.InitGroup()
        if self.system_input_group_id < 0:
            last_err = _last_error()
            raise ValueError(
                "Failed to initialize Telegraf system input group: {}".format(
                    last_err
                )
            )

    def _add_system_input(self, input_name):
        if input_name == 'net':
            c_array = (c_char_p * len(self.network_blacklist))()
            c_array[:] = [x.encode('utf-8') for x in self.network_blacklist]
            input_id = _lib.AddNetworkInput(
                self.system_input_group_id, c_array, len(c_array))
        elif input_name == 'disk':
            c_array = (c_char_p * len(self.disk_blacklist))()
            c_array[:] = [x.encode('utf-8') for x in self.disk_blacklist]
            input_id = _lib.AddDiskInput(
                self.system_input_group_id,
                c_char_p(self.disk_mount_point.encode('utf-8')),
                c_array,
                len(c_array),
            )
        elif input_name == 'diskio':
            c_array = (c_char_p * len(self.diskio_whitelist))()
            c_array[:] = [x.encode('utf-8') for x in self.diskio_whitelist]
            input_id = _lib.AddDiskIOInput(
                self.system_input_group_id, c_array, len(c_array))
        else:
            input_id = _lib.AddSimpleInput(
                self.system_input_group_id, input_name.encode('utf-8'))
        if input_id < 0:
            last_err = _last_error()
            raise ValueError(
                "_add_system_input has fail: {}".format(last_err)
            )

    def _add_simple_input(self, input_name, input_informations=None):
        input_id = _lib.AddSimpleInput(
            self.input_group_id, input_name.encode('utf-8'))
        if input_id >= 0:
            if input_informations is None:
                input_informations = {}
                input_informations["name"] = input_name
            else:
                try:
                    if input_informations["name"] != input_name:
                        raise ValueError(
                            "The input name mistmatch: {} != {}".format(
                                input_name, input_informations["name"]
                            )
                        )
                except KeyError:
                    input_informations["name"] = input_name
            self.inputs_id_map[input_id] = input_informations
        else:
            last_err = _last_error()
            raise ValueError(
                "_add_simple_input has fail: {}".format(last_err)
            )

    def _add_input_with_address(
            self, input_name, input_address, input_informations=None):
        input_id = _lib.AddInputWithAddress(
            self.input_group_id,
            input_name.encode('utf-8'),
            input_address.encode('utf-8')
        )
        if input_id >= 0:
            if input_informations is None:
                input_informations = {}
                input_informations["name"] = input_name
            else:
                try:
                    if input_informations["name"] != input_name:
                        raise ValueError(
                            "The input name mistmatch: {} != {}".format(
                                input_name, input_informations["name"]
                            )
                        )
                except KeyError:
                    input_informations["name"] = input_name
            self.inputs_id_map[input_id] = input_informations
        else:
            last_err = _last_error()
            raise ValueError(
                "_add_input_with_address has fail: {}".format(last_err)
            )

    def update_discovery(self, services):
        if _lib.FreeGroup(self.input_group_id) == -1:
            last_err = _last_error()
            raise ValueError("FreeGroup has fail: {}".format(last_err))
        self.inputs_id_map = {}
        self.input_group_id = _lib.InitGroup()
        if self.input_group_id < 0:
            last_err = _last_error()
            raise ValueError(
                "Failed to initialize Telegraf service input group: {}".format(
                    last_err
                )
            )
        for (service_name, instance) in services:
            input_informations = {}
            if service_name == "redis":
                input_informations["name"] = service_name
                input_informations["instance"] = instance
                service_info = services[(service_name, instance)]
                server_address = "tcp://%(address)s:%(port)s" % service_info
                self._add_input_with_address(
                    "redis", server_address, input_informations)

    def _init_system_inputs(self):
        self._add_system_input("cpu")
        self._add_system_input("disk")
        self._add_system_input("diskio")
        self._add_system_input("mem")
        self._add_system_input("net")
        self._add_system_input("process")
        self._add_system_input("swap")
        self._add_system_input("system")

    def gather_metrics(self):
        self._init_system_inputs()
        while not self.is_terminated.is_set():
            loop_start = time.time()
            metrics_vector = _lib.Gather(self.input_group_id)
            for i in range(0, metrics_vector.metric_point_count):
                metric_point = metrics_vector.metric_point[i]
                self.emit_metric(self._convert_metric_point(metric_point))
            _lib.FreeMetricPointVector(metrics_vector)

            system_metrics_vector = _lib.Gather(self.system_input_group_id)
            for i in range(0, system_metrics_vector.metric_point_count):
                metric_point = system_metrics_vector.metric_point[i]
                self.emit_metric(self._convert_metric_point(metric_point))
            _lib.FreeMetricPointVector(system_metrics_vector)

            delay = loop_start + 10 - time.time()
            self.is_terminated.wait(max(0, delay))

        if _lib.FreeGroup(self.system_input_group_id) == -1:
            last_err = _last_error()
            raise ValueError("FreeGroup has fail: {}".format(last_err))
        self.system_input_group_id = None
        if _lib.FreeGroup(self.input_group_id) == -1:
            last_err = _last_error()
            raise ValueError("FreeGroup has fail: {}".format(last_err))
        self.inputs_id_map = {}
        self.input_group_id = None

    def _convert_metric_point(self, metric_point):
        item = ""
        for j in range(0, metric_point.tag_count):
            tag = metric_point.tag[j]
            if (tag.tag_name).decode("utf-8") == "item":
                item = (tag.tag_value).decode("utf-8")
        if metric_point.name.decode('utf-8').startswith("redis"):
            item = self.inputs_id_map[metric_point.input_id]["instance"]

        labels = {}
        if item:
            labels['item'] = item
        return bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
            label=metric_point.name.decode("utf-8"),
            value=metric_point.value,
            labels=labels,
            time=metric_point.time,
        )
