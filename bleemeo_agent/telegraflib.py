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
import time
import bleemeo_agent.type


_lib = ctypes.cdll.LoadLibrary(
    "agentgo/libcabi.so")


class _Tag(ctypes.Structure):
    _fields_ = [('tag_name', ctypes.c_char_p),
                ('tag_value', ctypes.c_char_p)]


class _MetricPoint(ctypes.Structure):
    _fields_ = [('input_id', ctypes.c_int),
                ('name', ctypes.c_char_p),
                ('tag', ctypes.POINTER(_Tag)),
                ('tag_count', ctypes.c_int),
                ('metric_type', ctypes.c_int),
                ('value', ctypes.c_float),
                ('time', ctypes.c_int)]


class _MetricPointVector(ctypes.Structure):
    _fields_ = [('metric_point', ctypes.POINTER(_MetricPoint)),
                ('metric_point_count', ctypes.c_int)]


def _wrap_function(_lib, funcname, restype, argtypes):
    """Simplify wrapping ctypes functions"""
    func = _lib.__getattr__(funcname)
    func.restype = restype
    func.argtypes = argtypes
    return func


# Load function from C-lib
_init_group = _wrap_function(_lib, 'InitGroup', int, None)
_free_group = _wrap_function(
    _lib, 'FreeGroup', None, [ctypes.c_int, ])
_add_simple_input = _wrap_function(
    _lib, "AddSimpleInput", int, [ctypes.c_int, ctypes.c_char_p])
_add_input_with_address = _wrap_function(
    _lib, "AddInputWithAddress", int, [ctypes.c_int, ctypes.c_char_p, ctypes.c_char_p])
_gather = _wrap_function(_lib, 'Gather', _MetricPointVector, [ctypes.c_int, ])
_free_metric_point_vector = _wrap_function(
    _lib, 'FreeMetricPointVector', None, [_MetricPointVector, ])


class Telegraflib:

    def __init__(self, is_terminated=None, emit_metric=None):
        self.inputs_id_map = {}
        self.input_group_id = _init_group()
        self.system_inputs_id_map = {}
        self.system_input_group_id = _init_group()
        self.emit_metric = emit_metric
        self.is_terminated = is_terminated
        if self.input_group_id < 0:
            raise ValueError(
                "Impossible value for input_group_id: failed to initialize TelegrafLib")
        if self.system_input_group_id < 0:
            raise ValueError(
                "Impossible value for system_input_group_id: failed to initialize TelegrafLib")

    def _add_system_input(self, input_name, input_informations=None):
        input_id = _add_simple_input(
            self.system_input_group_id, input_name.encode('utf-8'))
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
            self.system_inputs_id_map[input_id] = input_informations
        else:
            raise ValueError(
                "Impossible value of input_id: _add_system_input has fail: {}".format(input_name))

    def _add_simple_input(self, input_name, input_informations=None):
        input_id = _add_simple_input(
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
        input_id = _add_input_with_address(
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
        _free_group(self.input_group_id)
        self.inputs_id_map = {}
        self.input_group_id = _init_group()
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
        while True:
            metrics_vector = _gather(self.input_group_id)
            for i in range(0, metrics_vector.metric_point_count):
                metric_point = metrics_vector.metric_point[i]
                self.emit_metric(self._convert_metric_point(metric_point))
            _free_metric_point_vector(metrics_vector)

            system_metrics_vector = _gather(self.system_input_group_id)
            for i in range(0, system_metrics_vector.metric_point_count):
                metric_point = system_metrics_vector.metric_point[i]
                self.emit_metric(self._convert_metric_point(metric_point))
            _free_metric_point_vector(system_metrics_vector)

            time.sleep(10)

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
