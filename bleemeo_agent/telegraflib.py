import ctypes
import time


lib = ctypes.cdll.LoadLibrary(
    "../agentgo/cabi.so")


class Tag(ctypes.Structure):
    _fields_ = [('tag_name', ctypes.c_char_p),
                ('tag_value', ctypes.c_char_p)]


class MetricPoint(ctypes.Structure):
    _fields_ = [('input_id', ctypes.c_int),
                ('name', ctypes.c_char_p),
                ('tag', ctypes.POINTER(ctypes.c_int)),
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


class Telegraplib:

    def __init__(self, is_terminated=None, emit_metric=None):
        self.inputs_map = {}
        self.input_group_id = init_input_group()
        self.emit_metric = emit_metric
        self.is_terminated = is_terminated

    def add_input(self, input_id):
        if isinstance(input_id, int) and input_id not in self.inputs_map:
            self.inputs_map[input_id] = {}
            return True
        return False

    def add_input_information(self, input_id, field, value):
        if input_id in self.inputs_map:
            input_informations = self.inputs_map[input_id]
        if (isinstance(input_informations, dict) and isinstance(field, str) and isinstance(value, str) and field not in input_informations):
            input_informations[field] = value
            self.inputs_map = input_informations
            return True
        return False

    def init_system_inputs(self):
        cpu_input_id = add_simple_input(self.input_group_id, "cpu")
        disk_input_id = add_simple_input(self.input_group_id, "disk")
        diskio_input_id = add_simple_input(self.input_group_id, "diskio")
        mem_input_id = add_simple_input(self.input_group_id, "mem")
        net_input_id = add_simple_input(self.input_group_id, "net")
        swap_input_id = add_simple_input(self.input_group_id, "swap")
        system_input_id = add_simple_input(self.input_group_id, "system")
        procstat_input_id = add_simple_input(self.input_group_id, "procstat")

        self.add_input(cpu_input_id)
        self.add_input_information(cpu_input_id, "name", "cpu")
        self.add_input(disk_input_id)
        self.add_input_information(disk_input_id, "name", "disk")
        self.add_input(diskio_input_id)
        self.add_input_information(diskio_input_id, "name", "diskio")
        self.add_input(mem_input_id)
        self.add_input_information(mem_input_id, "name", "mem")
        self.add_input(net_input_id)
        self.add_input_information(net_input_id, "name", "net")
        self.add_input(swap_input_id)
        self.add_input_information(swap_input_id, "name", "swap")
        self.add_input(system_input_id)
        self.add_input_information(system_input_id, "name", "system")
        self.add_input(procstat_input_id)
        self.add_input_information(procstat_input_id, "name", "procstat")

    def gather_metrics(self):
        metrics_vector = gather(self.input_group_id)
        print("\n-------------------------------------------------")
        for i in range(0, metrics_vector.metric_point_count):
            metric_point = (metrics_vector.metric_point[i])
            print(
                "Python: {}: {}".format(
                    metric_point.name, metric_point.value
                )
            )
        free_metric_point_vector(metrics_vector)

        time.sleep(10)


test = Telegraplib()
test.init_system_inputs()
test.gather_metrics()
