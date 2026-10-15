/******************************************************************************
Copyright (C) V1.0 2016  cdoyen@github https://github.com/cdoyen/xmlclean
This is a stripped down version for minipro by radiomanV@gitlab

This library is free software; you can redistribute it and/or
modify it under the terms of the GNU Lesser General Public
License as published by the Free Software Foundation; either
version 2.1 of the License, or (at your option) any later version.

This library is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
Lesser General Public License for more details.

You should have received a copy of the GNU Lesser General Public
License along with this library; if not, write to the Free Software
Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301  USA
******************************************************************************/

#ifndef __XML_H
#define __XML_H

#define _FILE_OFFSET_BITS 64

#include <stddef.h>

typedef struct {
	size_t z;
	const char *b;
} Memblock;

typedef struct {
	char *b;
	size_t i, g, e;
} MemMan;

typedef struct Parser {
	void *inputcbdata;
	int (*worker)(int type, const char *tag, size_t taglen,
				  struct Parser *parser);
	void *userdata;
	MemMan mm;
	size_t level;
	const char *content;
	size_t contentlen;
} Parser;

enum {
	XML_OK,
	ERRMEM,
	ERRHIERAR,
	ERREND
};
enum {
	SELFCLOSE_,
	COMMENT_,
	PROLOG_,
	NORMALCLOSE_,
	FRAMECLOSE_,
	OPENTAG_,
	UNKNOWN_
};

int parse(Parser *);
void done(Parser *);
Memblock get_attribute(const char *, size_t, const char *);
#endif
