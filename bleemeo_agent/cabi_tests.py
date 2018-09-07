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
gather = wrap_function(lib, 'Gather', MetricPointVector, [ctypes.c_int, ])
free_metric_point_vector = wrap_function(
    lib, 'FreeMetricPointVector', None, [MetricPointVector, ])

group_id = init_input_group()
cpu_id = add_simple_input(group_id, b"process")
while True:
    metrics_vector = gather(group_id)
    for i in range(0, metrics_vector.metric_point_count):
        metric_point = (metrics_vector.metric_point[i])
        print("Python: " + metric_point.name.decode("utf-8") +
              ": " + str(metric_point.value)
              )
    free_metric_point_vector(metrics_vector)
    print("\n")
    time.sleep(2)
