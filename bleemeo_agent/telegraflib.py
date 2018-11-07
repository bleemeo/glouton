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

import ctypes
from ctypes import (c_char_p, c_float, c_int, POINTER)
import os
import threading
import time

import bleemeo_agent.type


_lib = None
_lock = threading.Lock()


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
    global _lib
    with _lock:
        if _lib is not None:
            return
        if os.path.exists("agentgo/libcabi.so"):
            lib_path = "agentgo/libcabi.so"
        else:
            lib_path = "libcabi.so"
        _lib = ctypes.cdll.LoadLibrary(lib_path)

        _lib.InitGroup  # force load symbol
        _lib.FreeGroup.restype = None
        _lib.FreeGroup.argtypes = [c_int]
        _lib.AddSimpleInput.argtypes = [c_int, c_char_p]
        _lib.AddInputWithAddress.argtypes = [c_int, c_char_p, c_char_p]
        _lib.Gather.restype = _MetricPointVector
        _lib.Gather.argtypes = [c_int]
        _lib.FreeMetricPointVector.restype = None
        _lib.FreeMetricPointVector.argtypes = [_MetricPointVector]


class Telegraflib:

    def __init__(self, is_terminated, emit_metric):
        _init_lib()
        self.inputs_id_map = {}
        self.input_group_id = _lib.InitGroup()
        self.system_input_group_id = _lib.InitGroup()
        self.emit_metric = emit_metric
        self.is_terminated = is_terminated
        if self.input_group_id < 0:
            raise ValueError(
                "Impossible value for input_group_id: failed to initialize TelegrafLib")
        if self.system_input_group_id < 0:
            raise ValueError(
                "Impossible value for system_input_group_id: failed to initialize TelegrafLib")

    def _add_system_input(self, input_name):
        input_id = _lib.AddSimpleInput(
            self.system_input_group_id, input_name.encode('utf-8'))
        if input_id < 0:
            raise ValueError(
                "Impossible value of input_id: _add_system_input has fail: {}".format(input_name))

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
                        raise ValueError("The input name is different in the input_information_name: {} != {}".format(
                            input_name, input_informations["name"]))
                except KeyError:
                    input_informations["name"] = input_name
            self.inputs_id_map[input_id] = input_informations
        else:
            raise ValueError(
                "Impossible value of input_id: _add_simple_input has fail: {}".format(input_name))

    def _add_input_with_address(self, input_name, input_address, input_informations=None):
        input_id = _lib.AddInputWithAddress(
            self.input_group_id, input_name.encode('utf-8'), input_address.encode('utf-8'))
        if input_id >= 0:
            if input_informations is None:
                input_informations = {}
                input_informations["name"] = input_name
            else:
                try:
                    if input_informations["name"] != input_name:
                        raise ValueError("The input name is different in the input_information_name: {} != {}".format(
                            input_name, input_informations["name"]))
                except KeyError:
                    input_informations["name"] = input_name
            self.inputs_id_map[input_id] = input_informations
        else:
            raise ValueError(
                "Impossible value of input_id: _add_input_with_address has fail: {}".format(input_name))

    def update_discovery(self, services):
        _lib.FreeGroup(self.input_group_id)
        self.inputs_id_map = {}
        self.input_group_id = _lib.InitGroup()
        if self.input_group_id < 0:
            raise ValueError(
                "Impossible value for input_group_id: failed to initialize TelegrafLib")
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

    def _convert_metric_point(self, metric_point):
        item = ""
        for j in range(0, metric_point.tag_count):
            tag = metric_point.tag[j]
            if (tag.tag_name).decode("utf-8") == "item":
                item = (tag.tag_value).decode("utf-8")
        if metric_point.name.decode('utf-8').startswith("redis"):
            item = self.inputs_id_map[metric_point.input_id]["instance"]
        return bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
            label="go_" + (metric_point.name).decode("utf-8"),
            value=metric_point.value,
            item=item,
            time=metric_point.time,
        )
