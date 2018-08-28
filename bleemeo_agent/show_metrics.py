from ctypes import *
import time

lib = cdll.LoadLibrary(
    "../agentgo/src/agentgo/cabi/cabi.so")


def wrap_function(lib, funcname, restype, argtypes):
    """Simplify wrapping ctypes functions"""
    func = lib.__getattr__(funcname)
    func.restype = restype
    func.argtypes = argtypes
    return func


class MetricPoint(Structure):
    _fields_ = [('name', c_char_p),
                ('tag', POINTER(c_int)),
                ('tag_count', c_int),
                ('metric_type', c_int),
                ('value', c_float)]


class MetricPointVector(Structure):
    _fields_ = [('metric_point', POINTER(MetricPoint)),
                ('metric_point_count', c_int)]


# Load function from C-lib
init_input_group = wrap_function(lib, 'InitInputGroup', int, None)
add_simple_input = wrap_function(
    lib, "AddSimpleInput", int, [c_int, c_char_p])
add_input_with_address = wrap_function(
    lib, "AddInputWithAddress", int, [c_int, c_char_p, c_char_p])
gather = wrap_function(lib, 'Gather', MetricPointVector, [c_int, ])
free_metric_point_vector = wrap_function(
    lib, 'FreeMetricPointVector', None, [MetricPointVector, ])

# Create an input group
input_group_id = init_input_group()
# Add a memory and nginx input in the input group
memory_input_id = add_simple_input(input_group_id, "mem")
nginx_input_id = add_input_with_address(
    input_group_id, "nginx", "http://172.17.0.2/nginx_status")

while True:
    # Gather metric from input group
    metrics_vector = gather(input_group_id)

    print("\n-------------------------------------------------")
    for i in range(0, metrics_vector.metric_point_count):
        metric_point = (metrics_vector.metric_point[i])
        print(
            "{}: {}".format(
                metric_point.name, metric_point.value
            )
        )

    free_metric_point_vector(metrics_vector)

    time.sleep(2)
