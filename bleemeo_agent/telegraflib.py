import ctypes
import time
import bleemeo_agent.type


lib = ctypes.cdll.LoadLibrary(
    "agentgo/cabi.so")


class Tag(ctypes.Structure):
    _fields_ = [('tag_name', ctypes.c_char_p),
                ('tag_value', ctypes.c_char_p)]


class MetricPoint(ctypes.Structure):
    _fields_ = [('input_id', ctypes.c_int),
                ('name', ctypes.c_char_p),
                ('tag', ctypes.POINTER(Tag)),
                ('tag_count', ctypes.c_int),
                ('metric_type', ctypes.c_int),
                ('value', ctypes.c_float)]


class MetricPointVector(ctypes.Structure):
    _fields_ = [('metric_point', ctypes.POINTER(MetricPoint)),
                ('metric_point_count', ctypes.c_int)]


def wrap_function(lib, funcname, restype, argtypes):
    """Simplify wrapping ctypes functions"""
    func = lib.__getattr__(funcname)
    func.restype = restype
    func.argtypes = argtypes
    return func


# Load function from C-lib
init_input_group = wrap_function(lib, 'InitInputGroup', int, None)
add_simple_input = wrap_function(
    lib, "AddSimpleInput", int, [ctypes.c_int, ctypes.c_char_p])
add_input_with_address = wrap_function(
    lib, "AddInputWithAddress", int, [ctypes.c_int, ctypes.c_char_p, ctypes.c_char_p])
gather = wrap_function(lib, 'Gather', MetricPointVector, [ctypes.c_int, ])
free_metric_point_vector = wrap_function(
    lib, 'FreeMetricPointVector', None, [MetricPointVector, ])


class Telegraflib:

    def __init__(self, is_terminated=None, emit_metric=None):
        self.inputs_id_map = {}
        self.metric_to_compute = {}
        self.input_group_id = init_input_group()
        self.emit_metric = emit_metric
        self.is_terminated = is_terminated

    def add_simple_input(self, input_name, input_informations=None):
        input_id = add_simple_input(
            self.input_group_id, bytes(input_name, 'utf_8'))
        if input_id >= 0:
            if input_informations is None:
                input_informations = {}
                self.inputs_id_map[input_id] = input_informations
                try:
                    if input_informations["name"] != input_name:
                        raise ValueError("The input name is different in the input_information_name: {} != {}".format(
                            input_name, input_informations["name"]))
                except KeyError:
                    input_informations["name"] = input_name
            else:
                input_informations["name"] = input_name
        else:
            raise ValueError(
                "Impossible value of input_id: add_simple_input has fail: {}".format(input_name))

    def init_system_inputs(self):
        self.add_simple_input("mem")

    def gather_system_metrics(self):
        self.init_system_inputs()
        while True:
            metrics_vector = gather(self.input_group_id)
            for i in range(0, metrics_vector.metric_point_count):
                metric_point = (metrics_vector.metric_point[i])
                self.emit_metric(self.convert_metric_point(metric_point))
                """item = ""
                for j in range(0, metric_point.tag_count):
                    tag = (metric_point.tag[j])
                    if (tag.tag_name).decode("utf-8") == "item":
                        item = (tag.tag_value).decode("utf-8")
                """
            free_metric_point_vector(metrics_vector)
        time.sleep(10)

    def convert_metric_point(self, metric_point):
        for input_id, input_informations in self.inputs_id_map:
            if input_id == metric_point.input_id:
                input_name = input_informations["name"]
                if input_name == "mem":
                    return self.convert_mem_metric_point(metric_point)
                if input_name == "cpu":
                    return self.convert_cpu_metric_point(metric_point)
                else:
                    return bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                        label=(metric_point.name).decode("utf-8"),
                        time=time.time(),
                        value=metric_point.value,
                        item='',)

    def convert_mem_metric_point(self, metric_point):
        return bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
            label=(metric_point.name).decode("utf-8"),
            time=time.time(),
            value=metric_point.value,
            item='',)

    def convert_cpu_metric_point(self, metric_point):
        name = (metric_point.name).decode("utf_8")
        if name == "cpu_usage_iowait":
            name = "cpu_wait"
        elif name == "cpu_usage_irq":
            name = "cpu_interrupt"
        else:
            name = name.replace("_usage", "")
        if name in ["cpu_user", "cpu_system", "cpu_nice", "cpu_interrupt", "cpu_softirq", "cpu_steal"]:
            self.metric_to_compute[name] = metric_point.value

        return bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
            label=name,
            time=time.time(),
            value=metric_point.value,
            item='',)
