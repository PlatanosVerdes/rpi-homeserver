# Drop all acestream AttestationManager noise (licensing/attest failures) plus any
# traceback that follows one, up to the next real timestamped log line. Unrelated
# tracebacks are untouched because they are not preceded by an AttestationManager line.
/acestream\.AttestationManager\|/ { skip = 1; next }
skip {
    if ($0 ~ /^[0-9][0-9][0-9][0-9]-/) { skip = 0 }   # fresh log line: stop skipping, print it
    else { next }                                       # still inside the dropped block
}
{ print; fflush() }
