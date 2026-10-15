# SRAM Testing Example and Use Cases

We decided not to create a separate option for testing RAM: no one needs to
remember another option -- we already have one. Furthermore, the chip
interface may have specific features that we can test using the existing
logic test mechanism (in the future).

An example of how to run a test:

    $ minipro -p W24512 -T

Result on succes:

    Testing RAM of W24512: All tests passed.

When the socket is empty or the data bus contacts are broken or oxidized:

    Testing RAM of W24512: Data bus error

Let's short-circuit a pair of address pins with a screwdriver:

    Testing RAM of W24512: Short circuit detected at A6

Let's bend one address pin upwards:

    Testing RAM of W24512: Open circuit detected at A5

When opening a socket during testing:

    Testing RAM of W24512: Cell test failed at 0xXXXX

or

    Testing RAM of W24512: Increment test failed at 0xXXXX

When testing SRAM, the logic test is now silently skipped.
